package hooks_test

import (
	"errors"
	"io"
	"math/rand"
	"testing"

	"github.com/ipfs/go-graphsync"
	gsmsg "github.com/ipfs/go-graphsync/message"
	"github.com/ipfs/go-graphsync/responsemanager/hooks"
	"github.com/ipfs/go-graphsync/testutil"
	"github.com/ipld/go-ipld-prime"
	ipldfree "github.com/ipld/go-ipld-prime/impl/free"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/ipld/go-ipld-prime/traversal/selector/builder"
	peer "github.com/libp2p/go-libp2p-core/peer"
	"github.com/stretchr/testify/require"
)

type fakePersistenceOptions struct {
	po map[string]ipld.Loader
}

func (fpo *fakePersistenceOptions) GetLoader(name string) (ipld.Loader, bool) {
	loader, ok := fpo.po[name]
	return loader, ok
}

func TestRequestHookProcessing(t *testing.T) {
	fakeChooser := func(ipld.Link, ipld.LinkContext) (ipld.NodeBuilder, error) {
		return ipldfree.NodeBuilder(), nil
	}
	fakeLoader := func(link ipld.Link, lnkCtx ipld.LinkContext) (io.Reader, error) {
		return nil, nil
	}
	fpo := &fakePersistenceOptions{
		po: map[string]ipld.Loader{
			"chainstore": fakeLoader,
		},
	}
	extensionData := testutil.RandomBytes(100)
	extensionName := graphsync.ExtensionName("AppleSauce/McGee")
	extension := graphsync.ExtensionData{
		Name: extensionName,
		Data: extensionData,
	}
	extensionResponseData := testutil.RandomBytes(100)
	extensionResponse := graphsync.ExtensionData{
		Name: extensionName,
		Data: extensionResponseData,
	}

	root := testutil.GenerateCids(1)[0]
	requestID := graphsync.RequestID(rand.Int31())
	ssb := builder.NewSelectorSpecBuilder(ipldfree.NodeBuilder())
	request := gsmsg.NewRequest(requestID, root, ssb.Matcher().Node(), graphsync.Priority(0), extension)
	p := testutil.GeneratePeers(1)[0]
	testCases := map[string]struct {
		configure func(t *testing.T, requestHooks *hooks.IncomingRequestHooks)
		assert    func(t *testing.T, result hooks.RequestResult)
	}{
		"no hooks": {
			assert: func(t *testing.T, result hooks.RequestResult) {
				require.False(t, result.IsValidated)
				require.Empty(t, result.Extensions)
				require.Nil(t, result.CustomChooser)
				require.Nil(t, result.CustomLoader)
				require.NoError(t, result.Err)
			},
		},
		"sending extension data, no validation": {
			configure: func(t *testing.T, requestHooks *hooks.IncomingRequestHooks) {
				requestHooks.Register(func(p peer.ID, requestData graphsync.RequestData, hookActions graphsync.IncomingRequestHookActions) {
					hookActions.SendExtensionData(extensionResponse)
				})
			},
			assert: func(t *testing.T, result hooks.RequestResult) {
				require.False(t, result.IsValidated)
				require.Len(t, result.Extensions, 1)
				require.Contains(t, result.Extensions, extensionResponse)
				require.Nil(t, result.CustomChooser)
				require.Nil(t, result.CustomLoader)
				require.NoError(t, result.Err)
			},
		},
		"sending extension data, with validation": {
			configure: func(t *testing.T, requestHooks *hooks.IncomingRequestHooks) {
				requestHooks.Register(func(p peer.ID, requestData graphsync.RequestData, hookActions graphsync.IncomingRequestHookActions) {
					hookActions.ValidateRequest()
					hookActions.SendExtensionData(extensionResponse)
				})
			},
			assert: func(t *testing.T, result hooks.RequestResult) {
				require.True(t, result.IsValidated)
				require.Len(t, result.Extensions, 1)
				require.Contains(t, result.Extensions, extensionResponse)
				require.Nil(t, result.CustomChooser)
				require.Nil(t, result.CustomLoader)
				require.NoError(t, result.Err)
			},
		},
		"short circuit on error": {
			configure: func(t *testing.T, requestHooks *hooks.IncomingRequestHooks) {
				requestHooks.Register(func(p peer.ID, requestData graphsync.RequestData, hookActions graphsync.IncomingRequestHookActions) {
					hookActions.TerminateWithError(errors.New("something went wrong"))
				})
				requestHooks.Register(func(p peer.ID, requestData graphsync.RequestData, hookActions graphsync.IncomingRequestHookActions) {
					hookActions.ValidateRequest()
					hookActions.SendExtensionData(extensionResponse)
				})
			},
			assert: func(t *testing.T, result hooks.RequestResult) {
				require.False(t, result.IsValidated)
				require.Empty(t, result.Extensions)
				require.Nil(t, result.CustomChooser)
				require.Nil(t, result.CustomLoader)
				require.EqualError(t, result.Err, "something went wrong")
			},
		},
		"hooks unregistered": {
			configure: func(t *testing.T, requestHooks *hooks.IncomingRequestHooks) {
				unregister := requestHooks.Register(func(p peer.ID, requestData graphsync.RequestData, hookActions graphsync.IncomingRequestHookActions) {
					hookActions.ValidateRequest()
					hookActions.SendExtensionData(extensionResponse)
				})
				unregister()
			},
			assert: func(t *testing.T, result hooks.RequestResult) {
				require.False(t, result.IsValidated)
				require.Empty(t, result.Extensions)
				require.Nil(t, result.CustomChooser)
				require.Nil(t, result.CustomLoader)
				require.NoError(t, result.Err)
			},
		},
		"hooks alter the loader": {
			configure: func(t *testing.T, requestHooks *hooks.IncomingRequestHooks) {
				requestHooks.Register(func(p peer.ID, requestData graphsync.RequestData, hookActions graphsync.IncomingRequestHookActions) {
					if _, found := requestData.Extension(extensionName); found {
						hookActions.UsePersistenceOption("chainstore")
						hookActions.SendExtensionData(extensionResponse)
					}
				})
			},
			assert: func(t *testing.T, result hooks.RequestResult) {
				require.False(t, result.IsValidated)
				require.Len(t, result.Extensions, 1)
				require.Contains(t, result.Extensions, extensionResponse)
				require.Nil(t, result.CustomChooser)
				require.NotNil(t, result.CustomLoader)
				require.NoError(t, result.Err)
			},
		},
		"hooks alter to non-existent loader": {
			configure: func(t *testing.T, requestHooks *hooks.IncomingRequestHooks) {
				requestHooks.Register(func(p peer.ID, requestData graphsync.RequestData, hookActions graphsync.IncomingRequestHookActions) {
					if _, found := requestData.Extension(extensionName); found {
						hookActions.UsePersistenceOption("applesauce")
						hookActions.SendExtensionData(extensionResponse)
					}
				})
			},
			assert: func(t *testing.T, result hooks.RequestResult) {
				require.False(t, result.IsValidated)
				require.Len(t, result.Extensions, 1)
				require.Contains(t, result.Extensions, extensionResponse)
				require.Nil(t, result.CustomChooser)
				require.Nil(t, result.CustomLoader)
				require.EqualError(t, result.Err, "unknown loader option")
			},
		},
		"hooks alter the node builder chooser": {
			configure: func(t *testing.T, requestHooks *hooks.IncomingRequestHooks) {
				requestHooks.Register(func(p peer.ID, requestData graphsync.RequestData, hookActions graphsync.IncomingRequestHookActions) {
					if _, found := requestData.Extension(extensionName); found {
						hookActions.UseNodeBuilderChooser(fakeChooser)
						hookActions.SendExtensionData(extensionResponse)
					}
				})
			},
			assert: func(t *testing.T, result hooks.RequestResult) {
				require.False(t, result.IsValidated)
				require.Len(t, result.Extensions, 1)
				require.Contains(t, result.Extensions, extensionResponse)
				require.NotNil(t, result.CustomChooser)
				require.Nil(t, result.CustomLoader)
				require.NoError(t, result.Err)
			},
		},
	}
	for testCase, data := range testCases {
		t.Run(testCase, func(t *testing.T) {
			requestHooks := hooks.NewRequestHooks(fpo)
			if data.configure != nil {
				data.configure(t, requestHooks)
			}
			result := requestHooks.ProcessRequestHooks(p, request)
			if data.assert != nil {
				data.assert(t, result)
			}
		})
	}
}

type fakeBlkData struct {
	link ipld.Link
	size uint64
}

func (fbd fakeBlkData) Link() ipld.Link {
	return fbd.link
}

func (fbd fakeBlkData) BlockSize() uint64 {
	return fbd.size
}

func (fbd fakeBlkData) BlockSizeOnWire() uint64 {
	return fbd.size
}

func TestBlockHookProcessing(t *testing.T) {
	extensionData := testutil.RandomBytes(100)
	extensionName := graphsync.ExtensionName("AppleSauce/McGee")
	extension := graphsync.ExtensionData{
		Name: extensionName,
		Data: extensionData,
	}
	extensionResponseData := testutil.RandomBytes(100)
	extensionResponse := graphsync.ExtensionData{
		Name: extensionName,
		Data: extensionResponseData,
	}

	root := testutil.GenerateCids(1)[0]
	requestID := graphsync.RequestID(rand.Int31())
	ssb := builder.NewSelectorSpecBuilder(ipldfree.NodeBuilder())
	request := gsmsg.NewRequest(requestID, root, ssb.Matcher().Node(), graphsync.Priority(0), extension)
	p := testutil.GeneratePeers(1)[0]
	blockData := &fakeBlkData{
		link: cidlink.Link{Cid: testutil.GenerateCids(1)[0]},
		size: rand.Uint64(),
	}
	testCases := map[string]struct {
		configure func(t *testing.T, blockHooks *hooks.OutgoingBlockHooks)
		assert    func(t *testing.T, result hooks.BlockResult)
	}{
		"no hooks": {
			assert: func(t *testing.T, result hooks.BlockResult) {
				require.Empty(t, result.Extensions)
				require.NoError(t, result.Err)
			},
		},
		"send extension data": {
			configure: func(t *testing.T, blockHooks *hooks.OutgoingBlockHooks) {
				blockHooks.Register(func(p peer.ID, requestData graphsync.RequestData, blockData graphsync.BlockData, hookActions graphsync.OutgoingBlockHookActions) {
					hookActions.SendExtensionData(extensionResponse)
				})
			},
			assert: func(t *testing.T, result hooks.BlockResult) {
				require.Len(t, result.Extensions, 1)
				require.Contains(t, result.Extensions, extensionResponse)
				require.NoError(t, result.Err)
			},
		},
		"terminate with error": {
			configure: func(t *testing.T, blockHooks *hooks.OutgoingBlockHooks) {
				blockHooks.Register(func(p peer.ID, requestData graphsync.RequestData, blockData graphsync.BlockData, hookActions graphsync.OutgoingBlockHookActions) {
					hookActions.TerminateWithError(errors.New("failed"))
				})
			},
			assert: func(t *testing.T, result hooks.BlockResult) {
				require.Empty(t, result.Extensions)
				require.EqualError(t, result.Err, "failed")
			},
		},
		"pause response": {
			configure: func(t *testing.T, blockHooks *hooks.OutgoingBlockHooks) {
				blockHooks.Register(func(p peer.ID, requestData graphsync.RequestData, blockData graphsync.BlockData, hookActions graphsync.OutgoingBlockHookActions) {
					hookActions.PauseResponse()
				})
			},
			assert: func(t *testing.T, result hooks.BlockResult) {
				require.Empty(t, result.Extensions)
				require.EqualError(t, result.Err, hooks.ErrPaused.Error())
			},
		},
	}
	for testCase, data := range testCases {
		t.Run(testCase, func(t *testing.T) {
			blockHooks := hooks.NewBlockHooks()
			if data.configure != nil {
				data.configure(t, blockHooks)
			}
			result := blockHooks.ProcessBlockHooks(p, request, blockData)
			if data.assert != nil {
				data.assert(t, result)
			}
		})
	}
}