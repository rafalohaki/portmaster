package token

import (
	"testing"

	"github.com/safing/portmaster/base/modules"
	"github.com/safing/portmaster/service/core/pmtesting"
)

func TestMain(m *testing.M) {
	module := modules.Register("token", nil, nil, nil, "rng")
	pmtesting.TestMain(m, module)
}
