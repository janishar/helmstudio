package install

import (
	"context"
	"database/sql"
	"strings"

	"github.com/janishar/helmstudio/internal/manifest"
)

// Updating a studio: asking the repository where its ref points now, and
// building what it finds there.
//
// This is the one path that deliberately takes code nobody has approved yet.
// `:install` checks out the approved commit precisely so that a branch moving
// underneath cannot change what runs; an update asks for the move. So the
// commit is resolved before anything is fetched, and the API gates it on an
// approval naming that commit — the resolution lives here, and the gate lives
// where every other approval gate does.

// RemoteTip is the commit the manifest's ref points at in its repository,
// read without cloning anything.
//
// `ls-remote` is the whole of the network here: one connection, no objects
// fetched, nothing written to the checkout. --end-of-options because whatever
// the manifest says is a ref is a ref and never an option, which is the same
// rule the clone follows.
func (in *Installer) RemoteTip(ctx context.Context, m *manifest.Manifest) (string, error) {
	if m.LocalPath != "" {
		return "", refuse(KindBlocked, "%s is built from a directory on this Mac, not a repository, so there is nothing to pull", m.Name)
	}
	if m.Repo == "" {
		return "", refuse(KindBlocked, "%s names no repository to pull from", m.Name)
	}
	git, err := in.cfg.LookPath("git")
	if err != nil {
		return "", refuse(KindBlocked, "%s", toolMessage("git"))
	}
	ref := m.Ref
	if ref == "" {
		ref = "HEAD"
	}
	out, err := in.git(ctx, git, in.cfg.Dirs.Data(), in.cfg.Environ(), "ls-remote", "--end-of-options", m.Repo, ref)
	if err != nil {
		return "", refuse(KindUnavailable, "%s could not be reached: %v", m.Repo, err)
	}
	// "<sha>\trefs/heads/main". A ref that matches nothing prints nothing,
	// which is not a failure of the connection and must not read as one.
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	sha, _, ok := strings.Cut(line, "\t")
	if !ok || len(sha) < 7 {
		return "", refuse(KindBlocked, "%s has no ref %q", m.Repo, ref)
	}
	return sha, nil
}

// CheckUpdate asks where the ref points now and records it (02 §5:
// update_available is remote_sha != commit_sha, and without both fields the
// question is unanswerable).
//
// It runs when asked and never on a timer. What it costs is one connection to
// someone else's server, and helmstudio does not make that on its own
// schedule (docs/decisions.md 2026-09-19).
func (in *Installer) CheckUpdate(ctx context.Context, studioID string) (Info, error) {
	st, ok := in.cfg.Supervisor.Studio(studioID)
	if !ok {
		return Info{}, refuse(KindNotFound, "no studio %q is known", studioID)
	}
	m := st.Manifest
	tip, err := in.RemoteTip(ctx, m)
	if err != nil {
		return Info{}, err
	}
	err = in.cfg.Store.Update(ctx, func(ctx context.Context, tx *sql.Tx) error {
		r, exists, err := in.row(ctx, tx, studioID)
		if err != nil || !exists {
			return err
		}
		// Only a studio that is installed and idle says "update available".
		// One mid-install is about to answer the question itself, and one
		// that failed has a louder thing to say.
		state := r.state
		if (state == StateReady || state == StateUpdateAvailable) && r.commit != "" {
			if tip == r.commit {
				state = StateReady
			} else {
				state = StateUpdateAvailable
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE installations SET remote_sha = ?, remote_checked_at = ?, install_state = ?, updated_at = ? WHERE studio_id = ?`,
			tip, in.nowMs(), state, in.nowMs(), studioID)
		return err
	})
	if err != nil {
		return Info{}, err
	}
	return in.Info(ctx, studioID, m)
}

// Updatable answers whether an update would do anything, without starting
// one: there is a repository, the studio is installed, and its tip is not the
// commit already built.
//
// The API asks this before the approval gate, so that "you already have it"
// is said immediately rather than after a screen has been read and answered.
func (in *Installer) Updatable(ctx context.Context, studioID string) error {
	_, err := in.updateTarget(ctx, studioID)
	return err
}

// updateTarget is the commit an update would build, or why there is not one.
func (in *Installer) updateTarget(ctx context.Context, studioID string) (string, error) {
	st, ok := in.cfg.Supervisor.Studio(studioID)
	if !ok {
		return "", refuse(KindNotFound, "no studio %q is known", studioID)
	}
	m := st.Manifest
	tip, err := in.RemoteTip(ctx, m)
	if err != nil {
		return "", err
	}
	r, exists, err := in.row(ctx, in.cfg.Store.Reader(), studioID)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", refuse(KindBlocked, "%s is not installed, so there is nothing to update; install it instead", m.Name)
	}
	if r.commit == tip {
		return "", refuse(KindBlocked, "%s is already at %s, which is where %s points", m.Name, short(tip), refName(m))
	}
	return tip, nil
}

// Update fetches the manifest's ref again and builds what is at its tip.
//
// The tip is resolved first so that the refusals a person should see — there
// is no repository, the ref names nothing, it cannot be reached, you already
// have it — all happen before a job exists to watch.
func (in *Installer) Update(ctx context.Context, studioID string) (Job, error) {
	st, ok := in.cfg.Supervisor.Studio(studioID)
	if !ok {
		return Job{}, refuse(KindNotFound, "no studio %q is known", studioID)
	}
	_ = st
	if _, err := in.updateTarget(ctx, studioID); err != nil {
		return Job{}, err
	}
	return in.install(ctx, studioID, true)
}

// refName is the ref as the manifest writes it, for a sentence about it.
func refName(m *manifest.Manifest) string {
	if m.Ref == "" {
		return "its default branch"
	}
	return m.Ref
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
