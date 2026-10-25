package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requirePOSIXPermissions skips the test on Windows, where os.Chmod only
// toggles the read-only flag and the mode bits checkKeystorePermissions reads
// are not meaningful.
func requirePOSIXPermissions(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply on Windows")
	}
}

// writeKeystoreAt writes a keystore with an explicit mode. os.WriteFile's mode
// is masked by the umask, so the chmod afterwards is what makes these tests
// mean what they say.
func writeKeystoreAt(t *testing.T, mode os.FileMode, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "deployer.key")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600))
	require.NoError(t, os.Chmod(path, mode))
	return path
}

func TestCheckKeystorePermissionsAcceptsOwnerOnlyModes(t *testing.T) {
	requirePOSIXPermissions(t)

	// 0600 is what the README asks for; 0400 is a read-only secret mount.
	// Neither exposes the seed to any other account.
	for _, mode := range []os.FileMode{0o600, 0o400} {
		t.Run(fmt.Sprintf("%04o", mode), func(t *testing.T) {
			path := writeKeystoreAt(t, mode, "SABC\n")

			assert.NoError(t, checkKeystorePermissions(path, false))
		})
	}
}

func TestCheckKeystorePermissionsRejectsGroupOrOtherAccess(t *testing.T) {
	requirePOSIXPermissions(t)

	// The whole point of the check: an accidental `chmod 644` must not let
	// every account on the machine read the deployer seed that signs deploys.
	for _, mode := range []os.FileMode{0o640, 0o644, 0o604, 0o666} {
		t.Run(fmt.Sprintf("%04o", mode), func(t *testing.T) {
			const seed = "SSECRETSEEDMUSTNOTAPPEAR"
			path := writeKeystoreAt(t, mode, seed)

			err := checkKeystorePermissions(path, false)
			require.Error(t, err)

			// The message has to be actionable: which file, which mode, and
			// the exact fix.
			assert.ErrorContains(t, err, path)
			assert.ErrorContains(t, err, fmt.Sprintf("mode %04o", mode))
			assert.ErrorContains(t, err, "chmod 600")
			assert.ErrorContains(t, err, EnvAllowInsecureKeystore)

			// Errors end up in CI logs; the file's contents must not.
			assert.NotContains(t, err.Error(), seed)
		})
	}
}

func TestCheckKeystorePermissionsAllowInsecureAcceptsReadableFile(t *testing.T) {
	requirePOSIXPermissions(t)

	// Some container secret mounts have modes the operator cannot change. The
	// escape hatch must accept the file explicitly, and only when opted in.
	path := writeKeystoreAt(t, 0o644, "SABC\n")
	require.Error(t, checkKeystorePermissions(path, false))

	assert.NoError(t, checkKeystorePermissions(path, true))
}

func TestCheckKeystorePermissionsAllowInsecureEnvAcceptsReadableFile(t *testing.T) {
	requirePOSIXPermissions(t)

	path := writeKeystoreAt(t, 0o644, "SABC\n")
	t.Setenv(EnvAllowInsecureKeystore, "1")

	seed, _, err := Env{
		KeystorePath:          path,
		AllowInsecureKeystore: LoadEnv().AllowInsecureKeystore,
	}.SigningSeed()
	require.NoError(t, err)
	assert.Equal(t, "SABC", seed)
}

func TestCheckKeystorePermissionsReportsMissingFile(t *testing.T) {
	requirePOSIXPermissions(t)

	// A path that cannot be statted must not be treated as safely private;
	// report it rather than skipping the check.
	path := filepath.Join(t.TempDir(), "absent.key")

	err := checkKeystorePermissions(path, false)
	require.Error(t, err)
	assert.ErrorContains(t, err, path)
	assert.ErrorContains(t, err, "read keystore")
}
