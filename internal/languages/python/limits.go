package python

import (
	"overmindv/sandbox/internal/contracts"
	"overmindv/sandbox/internal/execution"
)

func MapLimits(limits contracts.Limits) execution.RuntimeLimits {
	return execution.RuntimeLimitsFromRequest(limits)
}
