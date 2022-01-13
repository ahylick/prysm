package forkchoice

import (
	"context"
	"fmt"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/golang/snappy"
	types "github.com/prysmaticlabs/eth2-types"
	"github.com/prysmaticlabs/prysm/beacon-chain/blockchain"
	mock "github.com/prysmaticlabs/prysm/beacon-chain/blockchain/testing"
	testDB "github.com/prysmaticlabs/prysm/beacon-chain/db/testing"
	"github.com/prysmaticlabs/prysm/beacon-chain/forkchoice/protoarray"
	"github.com/prysmaticlabs/prysm/beacon-chain/operations/attestations"
	"github.com/prysmaticlabs/prysm/beacon-chain/state"
	"github.com/prysmaticlabs/prysm/beacon-chain/state/stategen"
	v2 "github.com/prysmaticlabs/prysm/beacon-chain/state/v2"
	fieldparams "github.com/prysmaticlabs/prysm/config/fieldparams"
	"github.com/prysmaticlabs/prysm/config/params"
	ethpb "github.com/prysmaticlabs/prysm/proto/prysm/v1alpha1"
	"github.com/prysmaticlabs/prysm/proto/prysm/v1alpha1/block"
	"github.com/prysmaticlabs/prysm/proto/prysm/v1alpha1/wrapper"
	"github.com/prysmaticlabs/prysm/testing/require"
	"github.com/prysmaticlabs/prysm/testing/spectest/utils"
	"github.com/prysmaticlabs/prysm/testing/util"
)

type Step struct {
	Tick        *int    `json:"tick"`
	Block       *string `json:"block"`
	Attestation *string `json:"attestation"`
	Check       *Check  `json:"checks"`
}

type Check struct {
	Time                    int       `json:"time"`
	GenesisTime             int       `json:"genesis_time"`
	ProposerBoostRoot       string    `json:"proposer_boost_root"`
	Head                    SlotRoot  `json:"head"`
	JustifiedCheckPoint     EpochRoot `json:"justified_checkpoint"`
	BestJustifiedCheckPoint EpochRoot `json:"best_justified_checkpoint"`
	FinalizedCheckPoint     EpochRoot `json:"finalized_checkpoint"`
}

type SlotRoot struct {
	Slot int    `json:"slot"`
	Root string `json:"root"`
}

type EpochRoot struct {
	Epoch int    `json:"epoch"`
	Root  string `json:"root"`
}

// RunTest executes "forkchoice" test.
func RunTest(t *testing.T, config string) {
	require.NoError(t, utils.SetConfig(t, config))
	testFolders, testsFolderPath := utils.TestFolders(t, config, "altair", "fork_choice/on_block/pyspec_tests")
	for _, folder := range testFolders {
		t.Run(folder.Name(), func(t *testing.T) {
			ctx := context.Background()
			if folder.Name() != "basic" {
				t.Skip("skipping non-basic test")
			}
			file, err := util.BazelFileBytes(testsFolderPath, folder.Name(), "steps.yaml")
			require.NoError(t, err)
			var steps []Step
			require.NoError(t, utils.UnmarshalYaml(file, &steps), "Failed to Unmarshal")

			preBeaconStateFile, err := util.BazelFileBytes(testsFolderPath, folder.Name(), "anchor_state.ssz_snappy")
			require.NoError(t, err)
			preBeaconStateSSZ, err := snappy.Decode(nil /* dst */, preBeaconStateFile)
			require.NoError(t, err, "Failed to decompress")
			beaconStateBase := &ethpb.BeaconStateAltair{}
			require.NoError(t, beaconStateBase.UnmarshalSSZ(preBeaconStateSSZ), "Failed to unmarshal")
			beaconState, err := v2.InitializeFromProto(beaconStateBase)
			require.NoError(t, err)
			blockFile, err := util.BazelFileBytes(testsFolderPath, folder.Name(), "anchor_block.ssz_snappy")
			require.NoError(t, err)
			blockSSZ, err := snappy.Decode(nil /* dst */, blockFile)
			require.NoError(t, err, "Failed to decompress")
			block := &ethpb.BeaconBlockAltair{}
			require.NoError(t, block.UnmarshalSSZ(blockSSZ), "Failed to unmarshal")
			signed := &ethpb.SignedBeaconBlockAltair{Block: block, Signature: make([]byte, fieldparams.BLSSignatureLength)}
			b, err := wrapper.WrappedAltairSignedBeaconBlock(signed)
			require.NoError(t, err)

			service := newBlockchainService(t, beaconState, b)
			require.NoError(t, service.InitializeStore(ctx, beaconState, b))
			for _, step := range steps {
				if step.Tick != nil {
					require.NoError(t, service.OnTick(ctx, uint64(*step.Tick)))
				}
				if step.Block != nil {
					// Process block
					fileName := fmt.Sprint(*step.Block, ".ssz_snappy")
					blockFile, err := util.BazelFileBytes(testsFolderPath, folder.Name(), fileName)
					require.NoError(t, err)
					blockSSZ, err := snappy.Decode(nil /* dst */, blockFile)
					require.NoError(t, err, "Failed to decompress")
					block := &ethpb.SignedBeaconBlockAltair{}
					require.NoError(t, block.UnmarshalSSZ(blockSSZ), "Failed to unmarshal")
					r, err := block.Block.HashTreeRoot()
					require.NoError(t, err)
					wrappedBlock, err := wrapper.WrappedAltairSignedBeaconBlock(block)
					require.NoError(t, err)
					require.NoError(t, service.ReceiveBlock(ctx, wrappedBlock, r))
				}
				if step.Attestation != nil {
					// Process attestation
				}
				if step.Check != nil {
					c := step.Check
					require.Equal(t, uint64(c.Time), service.StoreTime())
					require.Equal(t, types.Slot(c.Head.Slot), service.HeadSlot())
					r, err := service.HeadRoot(ctx)
					require.NoError(t, err)
					require.DeepEqual(t, common.FromHex(c.Head.Root), r)
					cp := &ethpb.Checkpoint{
						Epoch: types.Epoch(c.JustifiedCheckPoint.Epoch),
						Root:  common.FromHex(c.JustifiedCheckPoint.Root),
					}
					require.DeepEqual(t, cp, service.JustifiedCheckpoint())
					cp = &ethpb.Checkpoint{
						Epoch: types.Epoch(c.BestJustifiedCheckPoint.Epoch),
						Root:  common.FromHex(c.BestJustifiedCheckPoint.Root),
					}
					require.DeepEqual(t, cp, service.BestJustifiedCheckpoint())
					cp = &ethpb.Checkpoint{
						Epoch: types.Epoch(c.FinalizedCheckPoint.Epoch),
						Root:  common.FromHex(c.FinalizedCheckPoint.Root),
					}
					require.DeepSSZEqual(t, cp, service.FinalizedCheckpoint())
				}
			}
		})
	}
}

func newBlockchainService(t *testing.T, st state.BeaconState, block block.SignedBeaconBlock) *blockchain.Service {
	d := testDB.SetupDB(t)
	ctx := context.Background()
	require.NoError(t, d.SaveBlock(ctx, block))
	r, err := block.Block().HashTreeRoot()
	require.NoError(t, err)
	require.NoError(t, d.SaveGenesisBlockRoot(ctx, r))
	require.NoError(t, d.SaveState(ctx, st, r))
	s, err := attestations.NewService(ctx, &attestations.Config{
		Pool: attestations.NewPool(),
	})
	require.NoError(t, err)

	opts := append([]blockchain.Option{},
		blockchain.WithFinalizedStateAtStartUp(st),
		blockchain.WithDatabase(d),
		blockchain.WithAttestationService(s),
		blockchain.WithForkChoiceStore(protoarray.New(0, 0, params.BeaconConfig().ZeroHash)),
		blockchain.WithStateGen(stategen.New(d)),
		blockchain.WithStateNotifier(&mock.MockStateNotifier{}),
		blockchain.WithAttestationPool(attestations.NewPool()),
	)
	service, err := blockchain.NewService(context.Background(), opts...)
	require.NoError(t, err)
	service.Start()
	return service
}