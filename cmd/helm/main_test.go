package main

import (
	"bytes"
	"runtime/debug"
	"strings"
	"testing"
)

func TestVersionNamesTheReleaseHelmWasBuiltAs(t *testing.T) {
	defer func(v string) { version = v }(version)
	version = "9.8.7-rc.1"
	for _, arg := range []string{"--version", "-v", "version"} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{arg}, &stdout, &stderr); code != 0 {
			t.Errorf("helm %s exited %d: %s", arg, code, stderr.String())
		}
		if got := stdout.String(); !strings.HasPrefix(got, "helm 9.8.7-rc.1") || strings.Count(got, "\n") != 1 {
			t.Errorf("helm %s printed %q, want one line naming helm 9.8.7-rc.1", arg, got)
		}
	}
}

func TestAHelmBuiltFromACloneSaysDev(t *testing.T) {
	var stdout bytes.Buffer
	run([]string{"--version"}, &stdout, &bytes.Buffer{})
	if !strings.HasPrefix(stdout.String(), "helm dev") {
		t.Errorf("helm --version printed %q, want helm dev when nothing set the version", stdout.String())
	}
}

func TestVersionLineNamesTheCommitTheBuildRecorded(t *testing.T) {
	settings := func(kv ...string) *debug.BuildInfo {
		info := &debug.BuildInfo{}
		for i := 0; i < len(kv); i += 2 {
			info.Settings = append(info.Settings, debug.BuildSetting{Key: kv[i], Value: kv[i+1]})
		}
		return info
	}
	cases := []struct {
		info *debug.BuildInfo
		want string
	}{
		{nil, "helm 1.0.0"},
		{settings(), "helm 1.0.0"},
		{settings("vcs.revision", "0123456789abcdef0123", "vcs.modified", "false"), "helm 1.0.0 (0123456789ab)"},
		{settings("vcs.revision", "0123456789abcdef0123", "vcs.modified", "true"), "helm 1.0.0 (0123456789ab, modified)"},
	}
	for _, c := range cases {
		if got := versionLine("1.0.0", c.info); got != c.want {
			t.Errorf("versionLine = %q, want %q", got, c.want)
		}
	}
}

func TestHelpIsTheUsageAndSucceeds(t *testing.T) {
	for _, arg := range []string{"--help", "-h", "help"} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{arg}, &stdout, &stderr); code != 0 {
			t.Errorf("helm %s exited %d", arg, code)
		}
		if !strings.Contains(stderr.String(), "usage: helm <command>") || !strings.Contains(stderr.String(), "--version") {
			t.Errorf("helm %s printed %q, want the usage, naming --version", arg, stderr.String())
		}
	}
}

func TestAnUnknownCommandIsAnError(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"versions"}, &bytes.Buffer{}, &stderr); code != 2 {
		t.Errorf("helm versions exited %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), `unknown command "versions"`) {
		t.Errorf("helm versions printed %q", stderr.String())
	}
}
