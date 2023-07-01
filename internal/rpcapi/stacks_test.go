package rpcapi

import (
	"context"
	"io"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/go-slug/sourceaddrs"
	"github.com/hashicorp/go-slug/sourcebundle"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/hashicorp/terraform/internal/rpcapi/terraform1"
	"github.com/hashicorp/terraform/internal/stacks/stackconfig"
)

func TestStacksOpenCloseStackConfiguration(t *testing.T) {
	ctx := context.Background()

	handles := newHandleTable()
	stacksServer := newStacksServer(handles)

	// In normal use a client would have previously opened a source bundle
	// using Dependencies.OpenSourceBundle, so we'll simulate the effect
	// of that here.
	var sourcesHnd handle[*sourcebundle.Bundle]
	{
		sources, err := sourcebundle.OpenDir("testdata/sourcebundle")
		if err != nil {
			t.Fatal(err)
		}
		sourcesHnd = handles.NewSourceBundle(sources)
	}

	openResp, err := stacksServer.OpenStackConfiguration(ctx, &terraform1.OpenStackConfiguration_Request{
		SourceBundleHandle: sourcesHnd.ForProtobuf(),
		SourceAddress: &terraform1.SourceAddress{
			Source: "git::https://example.com/foo.git",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// A client wouldn't normally be able to interact directly with the
	// stack configuration, but we're doing that here to simulate what would
	// happen in another service that takes configuration handles as input.
	{
		hnd := handle[*stackconfig.Config](openResp.StackConfigHandle)
		cfg := handles.StackConfig(hnd)
		if cfg == nil {
			t.Fatal("returned stack config handle is invalid")
		}
	}

	// A hypothetical attempt to close the underlying source bundle while
	// the stack configuration is active should fail.
	{
		depsServer := newDependenciesServer(handles)

		_, err := depsServer.CloseSourceBundle(ctx, &terraform1.CloseSourceBundle_Request{
			SourceBundleHandle: sourcesHnd.ForProtobuf(),
		})
		if err == nil {
			t.Fatal("successfully closed source bundle while stack config was using it; should have failed to close")
		}
		protoStatus, ok := status.FromError(err)
		if !ok {
			t.Fatal("error is not a protobuf status code")
		}
		if got, want := protoStatus.Code(), codes.InvalidArgument; got != want {
			t.Errorf("wrong error status\ngot:  %s\nwant: %s", got, want)
		}
		if got, want := protoStatus.Message(), "handle is in use by another open handle"; got != want {
			t.Errorf("wrong error message\ngot:  %s\nwant: %s", got, want)
		}
	}

	_, err = stacksServer.CloseStackConfiguration(ctx, &terraform1.CloseStackConfiguration_Request{
		StackConfigHandle: openResp.StackConfigHandle,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Should be able to close the source bundle now too.
	{
		depsServer := newDependenciesServer(handles)

		_, err := depsServer.CloseSourceBundle(ctx, &terraform1.CloseSourceBundle_Request{
			SourceBundleHandle: sourcesHnd.ForProtobuf(),
		})
		if err != nil {
			t.Fatalf("failed to close the source bundle: %s", err.Error())
		}
	}
}

func TestStacksFindStackConfigurationComponents(t *testing.T) {
	ctx := context.Background()

	handles := newHandleTable()
	stacksServer := newStacksServer(handles)

	// In normal use a client would have previously opened a source bundle
	// using Dependencies.OpenSourceBundle, so we'll simulate the effect
	// of that here.
	var sourcesHnd handle[*sourcebundle.Bundle]
	{
		sources, err := sourcebundle.OpenDir("testdata/sourcebundle")
		if err != nil {
			t.Fatal(err)
		}
		sourcesHnd = handles.NewSourceBundle(sources)
	}

	t.Run("empty config", func(t *testing.T) {
		openResp, err := stacksServer.OpenStackConfiguration(ctx, &terraform1.OpenStackConfiguration_Request{
			SourceBundleHandle: sourcesHnd.ForProtobuf(),
			SourceAddress: &terraform1.SourceAddress{
				Source: "git::https://example.com/foo.git",
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(openResp.Diagnostics) != 0 {
			t.Error("empty configuration generated diagnostics; expected none")
			if openResp.StackConfigHandle == 0 {
				return // Our later operations will fail if given the nil handle
			}
		}

		cmpntResp, err := stacksServer.FindStackConfigurationComponents(ctx, &terraform1.FindStackConfigurationComponents_Request{
			StackConfigHandle: openResp.StackConfigHandle,
		})
		if err != nil {
			t.Fatal(err)
		}

		got := cmpntResp.Config
		want := &terraform1.FindStackConfigurationComponents_StackConfig{
			// Intentionally empty, because the configuration we've loaded
			// is itself empty.
		}
		if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
			t.Errorf("wrong result\n%s", diff)
		}
	})
	t.Run("non-empty config", func(t *testing.T) {
		openResp, err := stacksServer.OpenStackConfiguration(ctx, &terraform1.OpenStackConfiguration_Request{
			SourceBundleHandle: sourcesHnd.ForProtobuf(),
			SourceAddress: &terraform1.SourceAddress{
				Source: "git::https://example.com/foo.git//non-empty-stack",
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(openResp.Diagnostics) != 0 {
			t.Error("empty configuration generated diagnostics; expected none")
			if openResp.StackConfigHandle == 0 {
				return // Our later operations will fail if given the nil handle
			}
		}

		cmpntResp, err := stacksServer.FindStackConfigurationComponents(ctx, &terraform1.FindStackConfigurationComponents_Request{
			StackConfigHandle: openResp.StackConfigHandle,
		})
		if err != nil {
			t.Fatal(err)
		}

		got := cmpntResp.Config
		want := &terraform1.FindStackConfigurationComponents_StackConfig{
			Components: map[string]*terraform1.FindStackConfigurationComponents_Component{
				"single": {
					SourceAddr: "git::https://example.com/foo.git//non-empty-stack/empty-module",
				},
				"for_each": {
					SourceAddr: "git::https://example.com/foo.git//non-empty-stack/empty-module",
					Instances:  terraform1.FindStackConfigurationComponents_FOR_EACH,
				},
			},
			EmbeddedStacks: map[string]*terraform1.FindStackConfigurationComponents_EmbeddedStack{
				"single": {
					SourceAddr: "git::https://example.com/foo.git//non-empty-stack/child",
					Config: &terraform1.FindStackConfigurationComponents_StackConfig{
						Components: map[string]*terraform1.FindStackConfigurationComponents_Component{
							"foo": {
								SourceAddr: "git::https://example.com/foo.git//non-empty-stack/empty-module",
							},
						},
					},
				},
				"for_each": {
					SourceAddr: "git::https://example.com/foo.git//non-empty-stack/child",
					Instances:  terraform1.FindStackConfigurationComponents_FOR_EACH,
					Config: &terraform1.FindStackConfigurationComponents_StackConfig{
						Components: map[string]*terraform1.FindStackConfigurationComponents_Component{
							"foo": {
								SourceAddr: "git::https://example.com/foo.git//non-empty-stack/empty-module",
							},
						},
					},
				},
			},
		}
		if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
			t.Errorf("wrong result\n%s", diff)
		}
	})
}

func TestStacksPlanStackChanges(t *testing.T) {
	ctx := context.Background()

	handles := newHandleTable()
	stacksServer := newStacksServer(handles)

	// TEMP: For now this RPC doesn't actually really do any work and instead
	// just immediately emits a warning diagnostic admitting that it's a liar.
	// We'll replace this with a real implementation in later commits.
	// This test implementation is equally fake, using invalid stubs of
	// everything just to get the right types for this to compile and
	// generate the expected warning.

	fakeSourceBundle := &sourcebundle.Bundle{}
	bundleHnd := handles.NewSourceBundle(fakeSourceBundle)
	emptyConfig := &stackconfig.Config{
		Root: &stackconfig.ConfigNode{
			Stack: &stackconfig.Stack{
				SourceAddr: sourceaddrs.MustParseSource("git::https://example.com/foo.git").(sourceaddrs.RemoteSource),
			},
		},
	}
	configHnd, err := handles.NewStackConfig(emptyConfig, bundleHnd)
	if err != nil {
		t.Fatal(err)
	}

	grpcClient, close := grpcClientForTesting(ctx, t, func(srv *grpc.Server) {
		terraform1.RegisterStacksServer(srv, stacksServer)
	})
	defer close()

	stacksClient := terraform1.NewStacksClient(grpcClient)
	events, err := stacksClient.PlanStackChanges(ctx, &terraform1.PlanStackChanges_Request{
		PlanMode:          terraform1.PlanMode_NORMAL,
		StackConfigHandle: configHnd.ForProtobuf(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	wantEvents := []*terraform1.PlanStackChanges_Event{
		{
			Event: &terraform1.PlanStackChanges_Event_Diagnostic{
				Diagnostic: &terraform1.Diagnostic{
					Severity: terraform1.Diagnostic_WARNING,
					Summary:  "Fake planning implementation",
					Detail:   "This plan contains no changes because this result was built from an early stub of the Terraform Core API for stack planning, which does not have any real logic for planning.",
				},
			},
		},
	}
	var gotEvents []*terraform1.PlanStackChanges_Event
	for {
		event, err := events.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}
		gotEvents = append(gotEvents, event)
	}

	if diff := cmp.Diff(wantEvents, gotEvents, protocmp.Transform()); diff != "" {
		t.Errorf("wrong events\n%s", diff)
	}
}
