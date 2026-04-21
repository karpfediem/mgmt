package resources

import "testing"

func TestSvcRefreshActionDefault(t *testing.T) {
	res := &SvcRes{}

	if got := res.refreshAction(); got != SvcRefreshActionReloadOrTryRestart {
		t.Fatalf("unexpected default refresh action: %q", got)
	}
}

func TestSvcValidateRefreshAction(t *testing.T) {
	testCases := []struct {
		name          string
		refreshAction string
		wantErr       bool
	}{
		{
			name:          "default",
			refreshAction: "",
		},
		{
			name:          "reload or try restart",
			refreshAction: SvcRefreshActionReloadOrTryRestart,
		},
		{
			name:          "try restart",
			refreshAction: SvcRefreshActionTryRestart,
		},
		{
			name:          "invalid",
			refreshAction: "reload",
			wantErr:       true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			res := &SvcRes{
				State:         "running",
				RefreshAction: tc.refreshAction,
			}

			err := res.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected validation error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}
