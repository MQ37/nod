package doctor

import "testing"

func TestRunReportsNamedChecks(t *testing.T) {
	checks := Run(Input{ConfigOK: true, ServiceInstalled: true, ServiceActive: true, ServerHealthy: true, HooksOK: true, ModelsOK: true, LibsOK: true})
	want := []string{"binary", "config", "models", "llama_cpp_libs", "service_installed", "server_health", "model_status", "model_idle_seconds", "hooks", "latency", "service_active"}
	if len(checks) != len(want) {
		t.Fatalf("got %d checks", len(checks))
	}
	for i, n := range want {
		if checks[i].Name != n {
			t.Fatalf("check %d=%s want %s", i, checks[i].Name, n)
		}
	}
	if OK(checks) {
		t.Fatal("missing binary must fail")
	}
}
