package sync

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/golang/snappy"
	"github.com/libp2p/go-libp2p-core/peer"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	pubsub_pb "github.com/libp2p/go-libp2p-pubsub/pb"
	types "github.com/prysmaticlabs/eth2-types"
	mockChain "github.com/prysmaticlabs/prysm/beacon-chain/blockchain/testing"
	"github.com/prysmaticlabs/prysm/beacon-chain/core/helpers"
	testingDB "github.com/prysmaticlabs/prysm/beacon-chain/db/testing"
	"github.com/prysmaticlabs/prysm/beacon-chain/p2p"
	"github.com/prysmaticlabs/prysm/beacon-chain/p2p/encoder"
	mockp2p "github.com/prysmaticlabs/prysm/beacon-chain/p2p/testing"
	p2ptypes "github.com/prysmaticlabs/prysm/beacon-chain/p2p/types"
	"github.com/prysmaticlabs/prysm/beacon-chain/state/stategen"
	mockSync "github.com/prysmaticlabs/prysm/beacon-chain/sync/initial-sync/testing"
	ethpb "github.com/prysmaticlabs/prysm/proto/prysm/v1alpha1"
	"github.com/prysmaticlabs/prysm/shared/bytesutil"
	"github.com/prysmaticlabs/prysm/shared/params"
	"github.com/prysmaticlabs/prysm/shared/testutil/assert"
	"github.com/prysmaticlabs/prysm/shared/testutil/require"
)

func TestService_ValidateSyncCommitteeMessage(t *testing.T) {
	beaconDB := testingDB.SetupDB(t)
	headRoot, keys := fillUpBlocksAndState(context.Background(), t, beaconDB)
	defaultTopic := p2p.SyncCommitteeSubnetTopicFormat
	fakeDigest := []byte{0xAB, 0x00, 0xCC, 0x9E}
	defaultTopic = defaultTopic + "/" + encoder.ProtocolSuffixSSZSnappy
	chainService := &mockChain.ChainService{
		Genesis:        time.Now(),
		ValidatorsRoot: [32]byte{'A'},
	}
	emptySig := [96]byte{}
	type args struct {
		ctx   context.Context
		pid   peer.ID
		msg   *ethpb.SyncCommitteeMessage
		topic string
	}
	tests := []struct {
		name     string
		svc      *Service
		setupSvc func(s *Service, msg *ethpb.SyncCommitteeMessage, topic string) (*Service, string)
		args     args
		want     pubsub.ValidationResult
	}{
		{
			name: "Is syncing",
			svc: NewService(context.Background(), &Config{
				P2P:               mockp2p.NewTestP2P(t),
				InitialSync:       &mockSync.Sync{IsSyncing: true},
				Chain:             chainService,
				StateNotifier:     chainService.StateNotifier(),
				OperationNotifier: chainService.OperationNotifier(),
			}),
			setupSvc: func(s *Service, msg *ethpb.SyncCommitteeMessage, topic string) (*Service, string) {
				s.cfg.StateGen = stategen.New(beaconDB)
				msg.BlockRoot = headRoot[:]
				s.cfg.DB = beaconDB
				s.initCaches()
				return s, topic
			},
			args: args{
				ctx:   context.Background(),
				pid:   "random",
				topic: "junk",
				msg: &ethpb.SyncCommitteeMessage{
					Slot:           1,
					ValidatorIndex: 1,
					BlockRoot:      params.BeaconConfig().ZeroHash[:],
					Signature:      emptySig[:],
				}},
			want: pubsub.ValidationIgnore,
		},
		{
			name: "Bad Topic",
			svc: NewService(context.Background(), &Config{
				P2P:               mockp2p.NewTestP2P(t),
				InitialSync:       &mockSync.Sync{IsSyncing: false},
				Chain:             chainService,
				StateNotifier:     chainService.StateNotifier(),
				OperationNotifier: chainService.OperationNotifier(),
			}),
			setupSvc: func(s *Service, msg *ethpb.SyncCommitteeMessage, topic string) (*Service, string) {
				s.cfg.StateGen = stategen.New(beaconDB)
				msg.BlockRoot = headRoot[:]
				s.cfg.DB = beaconDB
				s.initCaches()
				return s, topic
			},
			args: args{
				ctx:   context.Background(),
				pid:   "random",
				topic: "junk",
				msg: &ethpb.SyncCommitteeMessage{
					Slot:           1,
					ValidatorIndex: 1,
					BlockRoot:      params.BeaconConfig().ZeroHash[:],
					Signature:      emptySig[:],
				}},
			want: pubsub.ValidationReject,
		},
		{
			name: "Future Slot Message",
			svc: NewService(context.Background(), &Config{
				P2P:               mockp2p.NewTestP2P(t),
				InitialSync:       &mockSync.Sync{IsSyncing: false},
				Chain:             chainService,
				StateNotifier:     chainService.StateNotifier(),
				OperationNotifier: chainService.OperationNotifier(),
			}),
			setupSvc: func(s *Service, msg *ethpb.SyncCommitteeMessage, topic string) (*Service, string) {
				s.cfg.StateGen = stategen.New(beaconDB)
				s.cfg.DB = beaconDB
				s.initCaches()
				return s, topic
			},
			args: args{
				ctx:   context.Background(),
				pid:   "random",
				topic: fmt.Sprintf(defaultTopic, fakeDigest, 0),
				msg: &ethpb.SyncCommitteeMessage{
					Slot:           10,
					ValidatorIndex: 1,
					BlockRoot:      params.BeaconConfig().ZeroHash[:],
					Signature:      emptySig[:],
				}},
			want: pubsub.ValidationIgnore,
		},
		{
			name: "Already Seen Message",
			svc: NewService(context.Background(), &Config{
				P2P:               mockp2p.NewTestP2P(t),
				InitialSync:       &mockSync.Sync{IsSyncing: false},
				Chain:             chainService,
				StateNotifier:     chainService.StateNotifier(),
				OperationNotifier: chainService.OperationNotifier(),
			}),
			setupSvc: func(s *Service, msg *ethpb.SyncCommitteeMessage, topic string) (*Service, string) {
				s.cfg.StateGen = stategen.New(beaconDB)
				s.cfg.DB = beaconDB
				s.initCaches()

				s.setSeenSyncMessageIndexSlot(1, 1, 0)
				return s, topic
			},
			args: args{
				ctx:   context.Background(),
				pid:   "random",
				topic: fmt.Sprintf(defaultTopic, fakeDigest, 0),
				msg: &ethpb.SyncCommitteeMessage{
					Slot:           1,
					ValidatorIndex: 1,
					BlockRoot:      params.BeaconConfig().ZeroHash[:],
					Signature:      emptySig[:],
				}},
			want: pubsub.ValidationIgnore,
		},
		{
			name: "Non-existent block root",
			svc: NewService(context.Background(), &Config{
				P2P:               mockp2p.NewTestP2P(t),
				InitialSync:       &mockSync.Sync{IsSyncing: false},
				Chain:             chainService,
				StateNotifier:     chainService.StateNotifier(),
				OperationNotifier: chainService.OperationNotifier(),
			}),
			setupSvc: func(s *Service, msg *ethpb.SyncCommitteeMessage, topic string) (*Service, string) {
				s.cfg.StateGen = stategen.New(beaconDB)
				s.cfg.DB = beaconDB
				s.initCaches()
				s.cfg.Chain = &mockChain.ChainService{
					ValidatorsRoot: [32]byte{'A'},
					Genesis:        time.Now().Add(-time.Second * time.Duration(params.BeaconConfig().SecondsPerSlot) * time.Duration(10)),
				}
				incorrectRoot := [32]byte{0xBB}
				msg.BlockRoot = incorrectRoot[:]

				return s, topic
			},
			args: args{
				ctx:   context.Background(),
				pid:   "random",
				topic: fmt.Sprintf(defaultTopic, fakeDigest, 0),
				msg: &ethpb.SyncCommitteeMessage{
					Slot:           1,
					ValidatorIndex: 1,
					BlockRoot:      params.BeaconConfig().ZeroHash[:],
					Signature:      emptySig[:],
				}},
			want: pubsub.ValidationIgnore,
		},
		{
			name: "Subnet is non-existent",
			svc: NewService(context.Background(), &Config{
				P2P:               mockp2p.NewTestP2P(t),
				InitialSync:       &mockSync.Sync{IsSyncing: false},
				Chain:             chainService,
				StateNotifier:     chainService.StateNotifier(),
				OperationNotifier: chainService.OperationNotifier(),
			}),
			setupSvc: func(s *Service, msg *ethpb.SyncCommitteeMessage, topic string) (*Service, string) {
				s.cfg.StateGen = stategen.New(beaconDB)
				s.cfg.DB = beaconDB
				s.initCaches()
				msg.BlockRoot = headRoot[:]
				hState, err := beaconDB.State(context.Background(), headRoot)
				assert.NoError(t, err)
				s.cfg.Chain = &mockChain.ChainService{
					CurrentSyncCommitteeIndices: []types.CommitteeIndex{0},
					ValidatorsRoot:              [32]byte{'A'},
					Genesis:                     time.Now().Add(-time.Second * time.Duration(params.BeaconConfig().SecondsPerSlot) * time.Duration(hState.Slot()-1)),
				}
				numOfVals := hState.NumValidators()

				chosenVal := numOfVals - 10
				msg.Signature = emptySig[:]
				msg.BlockRoot = headRoot[:]
				msg.ValidatorIndex = types.ValidatorIndex(chosenVal)
				msg.Slot = helpers.PrevSlot(hState.Slot())

				// Set Bad Topic and Subnet
				digest, err := s.currentForkDigest()
				assert.NoError(t, err)
				actualTopic := fmt.Sprintf(defaultTopic, digest, 5)

				return s, actualTopic
			},
			args: args{
				ctx:   context.Background(),
				pid:   "random",
				topic: defaultTopic,
				msg: &ethpb.SyncCommitteeMessage{
					Slot:           1,
					ValidatorIndex: 1,
					BlockRoot:      params.BeaconConfig().ZeroHash[:],
					Signature:      emptySig[:],
				}},
			want: pubsub.ValidationReject,
		},
		{
			name: "Validator is non-existent",
			svc: NewService(context.Background(), &Config{
				P2P:               mockp2p.NewTestP2P(t),
				InitialSync:       &mockSync.Sync{IsSyncing: false},
				Chain:             chainService,
				StateNotifier:     chainService.StateNotifier(),
				OperationNotifier: chainService.OperationNotifier(),
			}),
			setupSvc: func(s *Service, msg *ethpb.SyncCommitteeMessage, topic string) (*Service, string) {
				s.cfg.StateGen = stategen.New(beaconDB)
				s.cfg.DB = beaconDB
				s.initCaches()
				msg.BlockRoot = headRoot[:]
				hState, err := beaconDB.State(context.Background(), headRoot)
				assert.NoError(t, err)
				s.cfg.Chain = &mockChain.ChainService{
					ValidatorsRoot: [32]byte{'A'},
					Genesis:        time.Now().Add(-time.Second * time.Duration(params.BeaconConfig().SecondsPerSlot) * time.Duration(hState.Slot()-1)),
				}

				numOfVals := hState.NumValidators()

				chosenVal := numOfVals + 10
				msg.Signature = emptySig[:]
				msg.BlockRoot = headRoot[:]
				msg.ValidatorIndex = types.ValidatorIndex(chosenVal)
				msg.Slot = helpers.PrevSlot(hState.Slot())

				return s, topic
			},
			args: args{
				ctx:   context.Background(),
				pid:   "random",
				topic: defaultTopic,
				msg: &ethpb.SyncCommitteeMessage{
					Slot:           1,
					ValidatorIndex: 1,
					BlockRoot:      params.BeaconConfig().ZeroHash[:],
					Signature:      emptySig[:],
				}},
			want: pubsub.ValidationIgnore,
		},
		{
			name: "Invalid Sync Committee Signature",
			svc: NewService(context.Background(), &Config{
				P2P:               mockp2p.NewTestP2P(t),
				InitialSync:       &mockSync.Sync{IsSyncing: false},
				Chain:             chainService,
				StateNotifier:     chainService.StateNotifier(),
				OperationNotifier: chainService.OperationNotifier(),
			}),
			setupSvc: func(s *Service, msg *ethpb.SyncCommitteeMessage, topic string) (*Service, string) {
				s.cfg.StateGen = stategen.New(beaconDB)
				s.cfg.DB = beaconDB
				s.initCaches()
				msg.BlockRoot = headRoot[:]
				hState, err := beaconDB.State(context.Background(), headRoot)
				assert.NoError(t, err)

				numOfVals := hState.NumValidators()

				chosenVal := numOfVals - 10
				msg.Signature = emptySig[:]
				msg.BlockRoot = headRoot[:]
				msg.ValidatorIndex = types.ValidatorIndex(chosenVal)
				msg.Slot = helpers.PrevSlot(hState.Slot())

				d, err := helpers.Domain(hState.Fork(), helpers.SlotToEpoch(hState.Slot()), params.BeaconConfig().DomainSyncCommittee, hState.GenesisValidatorRoot())
				assert.NoError(t, err)
				subCommitteeSize := params.BeaconConfig().SyncCommitteeSize / params.BeaconConfig().SyncCommitteeSubnetCount
				s.cfg.Chain = &mockChain.ChainService{
					CurrentSyncCommitteeIndices: []types.CommitteeIndex{types.CommitteeIndex(subCommitteeSize)},
					ValidatorsRoot:              [32]byte{'A'},
					Genesis:                     time.Now().Add(-time.Second * time.Duration(params.BeaconConfig().SecondsPerSlot) * time.Duration(hState.Slot()-1)),
					SyncCommitteeDomain:         d,
					PublicKey:                   bytesutil.ToBytes48(keys[chosenVal].PublicKey().Marshal()),
				}

				// Set Topic and Subnet
				digest, err := s.currentForkDigest()
				assert.NoError(t, err)
				actualTopic := fmt.Sprintf(defaultTopic, digest, 1)

				return s, actualTopic
			},
			args: args{
				ctx:   context.Background(),
				pid:   "random",
				topic: defaultTopic,
				msg: &ethpb.SyncCommitteeMessage{
					Slot:           1,
					ValidatorIndex: 1,
					BlockRoot:      params.BeaconConfig().ZeroHash[:],
					Signature:      emptySig[:],
				}},
			want: pubsub.ValidationReject,
		},
		{
			name: "Valid Sync Committee Signature",
			svc: NewService(context.Background(), &Config{
				P2P:               mockp2p.NewTestP2P(t),
				InitialSync:       &mockSync.Sync{IsSyncing: false},
				Chain:             chainService,
				StateNotifier:     chainService.StateNotifier(),
				OperationNotifier: chainService.OperationNotifier(),
			}),
			setupSvc: func(s *Service, msg *ethpb.SyncCommitteeMessage, topic string) (*Service, string) {
				s.cfg.StateGen = stategen.New(beaconDB)
				s.cfg.DB = beaconDB
				s.initCaches()
				msg.BlockRoot = headRoot[:]
				hState, err := beaconDB.State(context.Background(), headRoot)
				assert.NoError(t, err)
				subCommitteeSize := params.BeaconConfig().SyncCommitteeSize / params.BeaconConfig().SyncCommitteeSubnetCount

				numOfVals := hState.NumValidators()

				chosenVal := numOfVals - 10
				d, err := helpers.Domain(hState.Fork(), helpers.SlotToEpoch(hState.Slot()), params.BeaconConfig().DomainSyncCommittee, hState.GenesisValidatorRoot())
				assert.NoError(t, err)
				rawBytes := p2ptypes.SSZBytes(headRoot[:])
				sigRoot, err := helpers.ComputeSigningRoot(&rawBytes, d)
				assert.NoError(t, err)

				s.cfg.Chain = &mockChain.ChainService{
					CurrentSyncCommitteeIndices: []types.CommitteeIndex{types.CommitteeIndex(subCommitteeSize)},
					ValidatorsRoot:              [32]byte{'A'},
					Genesis:                     time.Now().Add(-time.Second * time.Duration(params.BeaconConfig().SecondsPerSlot) * time.Duration(hState.Slot()-1)),
					SyncCommitteeDomain:         d,
					PublicKey:                   bytesutil.ToBytes48(keys[chosenVal].PublicKey().Marshal()),
				}

				msg.Signature = keys[chosenVal].Sign(sigRoot[:]).Marshal()
				msg.BlockRoot = headRoot[:]
				msg.ValidatorIndex = types.ValidatorIndex(chosenVal)
				msg.Slot = helpers.PrevSlot(hState.Slot())

				// Set Topic and Subnet
				digest, err := s.currentForkDigest()
				assert.NoError(t, err)
				actualTopic := fmt.Sprintf(defaultTopic, digest, 1)

				return s, actualTopic
			},
			args: args{
				ctx:   context.Background(),
				pid:   "random",
				topic: defaultTopic,
				msg: &ethpb.SyncCommitteeMessage{
					Slot:           1,
					ValidatorIndex: 1,
					BlockRoot:      params.BeaconConfig().ZeroHash[:],
					Signature:      emptySig[:],
				}},
			want: pubsub.ValidationAccept,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.svc, tt.args.topic = tt.setupSvc(tt.svc, tt.args.msg, tt.args.topic)
			marshalledObj, err := tt.args.msg.MarshalSSZ()
			assert.NoError(t, err)
			marshalledObj = snappy.Encode(nil, marshalledObj)
			msg := &pubsub.Message{
				Message: &pubsub_pb.Message{
					Data:  marshalledObj,
					Topic: &tt.args.topic,
				},
				ReceivedFrom:  "",
				ValidatorData: nil,
			}
			if got := tt.svc.validateSyncCommitteeMessage(tt.args.ctx, tt.args.pid, msg); got != tt.want {
				t.Errorf("validateSyncCommitteeMessage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestService_ignoreHasSeenSyncMsg(t *testing.T) {
	tests := []struct {
		name      string
		setupSvc  func(s *Service, msg *ethpb.SyncCommitteeMessage, topic string) (*Service, string)
		msg       *ethpb.SyncCommitteeMessage
		committee []types.CommitteeIndex
		want      pubsub.ValidationResult
	}{
		{
			name: "has seen",
			setupSvc: func(s *Service, msg *ethpb.SyncCommitteeMessage, topic string) (*Service, string) {
				s.initCaches()
				s.setSeenSyncMessageIndexSlot(1, 0, 0)
				return s, ""
			},
			msg:       &ethpb.SyncCommitteeMessage{ValidatorIndex: 0, Slot: 1},
			committee: []types.CommitteeIndex{1, 2, 3},
			want:      pubsub.ValidationIgnore,
		},
		{
			name: "has not seen",
			setupSvc: func(s *Service, msg *ethpb.SyncCommitteeMessage, topic string) (*Service, string) {
				s.initCaches()
				s.setSeenSyncMessageIndexSlot(1, 0, 0)
				return s, ""
			},
			msg:       &ethpb.SyncCommitteeMessage{ValidatorIndex: 1, Slot: 1},
			committee: []types.CommitteeIndex{1, 2, 3},
			want:      pubsub.ValidationAccept,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Service{}
			s, _ = tt.setupSvc(s, tt.msg, "")
			f := s.ignoreHasSeenSyncMsg(tt.msg, tt.committee)
			result := f(context.Background())
			require.Equal(t, tt.want, result)
		})
	}
}

func TestService_rejectIncorrectSyncCommittee(t *testing.T) {
	tests := []struct {
		name             string
		cfg              *Config
		setupTopic       func(s *Service) string
		committeeIndices []types.CommitteeIndex
		want             pubsub.ValidationResult
	}{
		{
			name: "invalid",
			cfg: &Config{
				Chain: &mockChain.ChainService{
					Genesis:        time.Now(),
					ValidatorsRoot: [32]byte{1},
				},
			},
			committeeIndices: []types.CommitteeIndex{0},
			setupTopic: func(_ *Service) string {
				return "foobar"
			},
			want: pubsub.ValidationReject,
		},
		{
			name: "valid",
			cfg: &Config{
				Chain: &mockChain.ChainService{
					Genesis:        time.Now(),
					ValidatorsRoot: [32]byte{1},
				},
			},
			committeeIndices: []types.CommitteeIndex{0},
			setupTopic: func(s *Service) string {
				format := p2p.GossipTypeMapping[reflect.TypeOf(&ethpb.SyncCommitteeMessage{})]
				digest, err := s.currentForkDigest()
				require.NoError(t, err)
				prefix := fmt.Sprintf(format, digest, 0 /* validator index 0 */)
				topic := prefix + "foobar"
				return topic
			},
			want: pubsub.ValidationAccept,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Service{
				cfg: tt.cfg,
			}
			topic := tt.setupTopic(s)
			f := s.rejectIncorrectSyncCommittee(tt.committeeIndices, topic)
			result := f(context.Background())
			require.Equal(t, tt.want, result)
		})
	}
}

func Test_ignoreEmptyCommittee(t *testing.T) {
	tests := []struct {
		name      string
		committee []types.CommitteeIndex
		want      pubsub.ValidationResult
	}{
		{
			name:      "nil",
			committee: nil,
			want:      pubsub.ValidationIgnore,
		},
		{
			name:      "empty",
			committee: []types.CommitteeIndex{},
			want:      pubsub.ValidationIgnore,
		},
		{
			name:      "non-empty",
			committee: []types.CommitteeIndex{1},
			want:      pubsub.ValidationAccept,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := ignoreEmptyCommittee(tt.committee)
			result := f(context.Background())
			require.Equal(t, tt.want, result)
		})
	}
}