package mcp

import (
	"os"
	"runtime"
	"strings"
)

// safeEnvNames lists environment variables a stdio MCP server inherits by
// default. Everything else (API keys, cloud credentials, tokens) must be passed
// explicitly with WithEnv or by opting in with WithInheritEnv.
var safeEnvNames = map[string]struct{}{
	"PATH": {}, "HOME": {}, "LANG": {}, "TMPDIR": {}, "USER": {}, "SHELL": {},
	"LOGNAME": {}, "TZ": {}, "TERM": {},
}

// safeEnvNamesWindows is matched case-insensitively.
var safeEnvNamesWindows = map[string]struct{}{
	"SYSTEMROOT": {}, "SYSTEMDRIVE": {}, "PATH": {}, "PATHEXT": {}, "COMSPEC": {},
	"TEMP": {}, "TMP": {}, "USERNAME": {}, "USERPROFILE": {}, "HOMEDRIVE": {},
	"HOMEPATH": {}, "APPDATA": {}, "LOCALAPPDATA": {}, "PROGRAMDATA": {},
	"PROGRAMFILES": {}, "PROGRAMFILES(X86)": {}, "WINDIR": {}, "OS": {},
	"PROCESSOR_ARCHITECTURE": {}, "NUMBER_OF_PROCESSORS": {},
}

func envAllowed(name, goos string) bool {
	if goos == "windows" {
		_, ok := safeEnvNamesWindows[strings.ToUpper(name)]
		return ok
	}
	if _, ok := safeEnvNames[name]; ok {
		return true
	}
	return strings.HasPrefix(name, "LC_")
}

// buildEnv returns the environment for an MCP server subprocess. With inherit
// false only allowlisted variables of parent are kept; explicit entries are
// always appended and win over inherited values.
func buildEnv(parent []string, goos string, inherit bool, explicit []string) []string {
	out := make([]string, 0, len(parent)+len(explicit))
	for _, kv := range parent {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue
		}
		if inherit || envAllowed(name, goos) {
			out = append(out, kv)
		}
	}
	return append(out, explicit...)
}

func subprocessEnv(inherit bool, explicit []string) []string {
	return buildEnv(os.Environ(), runtime.GOOS, inherit, explicit)
}
