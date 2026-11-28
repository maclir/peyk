package agent

import "testing"

func TestStayAwakeAcquiresAndReleasesAssertions(t *testing.T) {
	idle, sys := holdPowerAssertions()
	if idle == 0 || sys == 0 {
		t.Fatalf("assertions = (%d, %d), want non-zero", idle, sys)
	}
	releasePowerAssertion(idle)
	releasePowerAssertion(sys)

	release := StayAwake()
	release()
	release()
}
