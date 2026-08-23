// Server-side reporting for repo-app builds/updates.
//
// TODO(M2): implement the app_center client — POST repo_builds status
// transitions and installed_sha back to the server after install /
// update / failed runs (TechPlan §3.2 client.go + §5.2). Pattern to
// follow: internal/skillsync/client.go (Bearer auth + sentinel errors +
// httptest-based tests); token resolution via wiring.TokenProviderFor
// (cmd/biu/main.go). Deliberately a stub in M1: the local runner is
// fully functional standalone.

package repoapp
