// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: BUSL-1.1

package terraform

import (
	"time"

	"github.com/zclconf/go-cty/cty"

	"github.com/hashicorp/terraform/internal/addrs"
	"github.com/hashicorp/terraform/internal/configs"
	"github.com/hashicorp/terraform/internal/instances"
	"github.com/hashicorp/terraform/internal/lang"
	"github.com/hashicorp/terraform/internal/namedvals"
	"github.com/hashicorp/terraform/internal/plans"
	"github.com/hashicorp/terraform/internal/plans/deferring"
	"github.com/hashicorp/terraform/internal/states"
)

// Evaluator provides the necessary contextual data for evaluating expressions
// for a particular walk operation.
type Evaluator struct {
	// Operation defines what type of operation this evaluator is being used
	// for.
	Operation walkOperation

	// Meta is contextual metadata about the current operation.
	Meta *ContextMeta

	// Config is the root node in the configuration tree.
	Config *configs.Config

	// Instances tracks the dynamic instances that are associated with each
	// module call or resource. The graph walk gradually registers the
	// set of instances for each object within the graph nodes for those
	// objects, and so as long as the graph has been built correctly the
	// set of instances for an object should always be available by the time
	// we're evaluating expressions that refer to it.
	Instances *instances.Expander

	// NamedValues is where we keep the values of already-evaluated input
	// variables, local values, and output values.
	NamedValues *namedvals.State

	// Deferrals tracks resources and modules that have had either their
	// expansion or their specific planned actions deferred to a future
	// plan/apply round.
	Deferrals *deferring.Deferred

	// Plugins is the library of available plugin components (providers and
	// provisioners) that we have available to help us evaluate expressions
	// that interact with plugin-provided objects.
	//
	// From this we only access the schemas of the plugins, and don't otherwise
	// interact with plugin instances.
	Plugins *contextPlugins

	// State is the current state, embedded in a wrapper that ensures that
	// it can be safely accessed and modified concurrently.
	State *states.SyncState

	// Changes is the set of proposed changes, embedded in a wrapper that
	// ensures they can be safely accessed and modified concurrently.
	Changes *plans.ChangesSync

	PlanTimestamp time.Time
}

// Scope creates an evaluation scope for the given module path and optional
// resource.
//
// If the "self" argument is nil then the "self" object is not available
// in evaluated expressions. Otherwise, it behaves as an alias for the given
// address.
func (e *Evaluator) Scope(data lang.Data, self addrs.Referenceable, source addrs.Referenceable, extFuncs lang.ExternalFuncs) *lang.Scope {
	return &lang.Scope{
		Data:          data,
		ParseRef:      addrs.ParseRef,
		SelfAddr:      self,
		SourceAddr:    source,
		PureOnly:      e.Operation != walkApply && e.Operation != walkDestroy && e.Operation != walkEval,
		BaseDir:       ".", // Always current working directory for now.
		PlanTimestamp: e.PlanTimestamp,
		ExternalFuncs: extFuncs,
	}
}

// InstanceKeyEvalData is the old name for instances.RepetitionData, aliased
// here for compatibility. In new code, use instances.RepetitionData instead.
type InstanceKeyEvalData = instances.RepetitionData

// EvalDataForInstanceKey constructs a suitable InstanceKeyEvalData for
// evaluating in a context that has the given instance key.
//
// The forEachMap argument can be nil when preparing for evaluation
// in a context where each.value is prohibited, such as a destroy-time
// provisioner. In that case, the returned EachValue will always be
// cty.NilVal.
func EvalDataForInstanceKey(key addrs.InstanceKey, forEachMap map[string]cty.Value) InstanceKeyEvalData {
	var evalData InstanceKeyEvalData
	if key == nil {
		return evalData
	}

	keyValue := key.Value()
	switch keyValue.Type() {
	case cty.String:
		evalData.EachKey = keyValue
		evalData.EachValue = forEachMap[keyValue.AsString()]
	case cty.Number:
		evalData.CountIndex = keyValue
	}
	return evalData
}

// EvalDataForNoInstanceKey is a value of InstanceKeyData that sets no instance
// key values at all, suitable for use in contexts where no keyed instance
// is relevant.
var EvalDataForNoInstanceKey = InstanceKeyEvalData{}
