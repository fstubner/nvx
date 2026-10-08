package nvx

import (
	"strings"
	"testing"
)

// A grant recorded with its directory's ID is followed to where the directory is
// now, so a deleted folder is a finished reset and a renamed one is withdrawn at
// its new name. A record with no ID keeps the behaviour it had.

func withLocator(t *testing.T, f func(g readExecGrant) (string, directoryLocation)) {
	t.Helper()
	saved := locateDirectory
	locateDirectory = f
	t.Cleanup(func() { locateDirectory = saved })
}

func TestAGrantedFolderThatWasDeletedIsAFinishedReset(t *testing.T) {
	withLocator(t, func(readExecGrant) (string, directoryLocation) { return "", locationGone })
	var revoked []string
	out := revokeAllReadExecGrantsWithin(
		[]readExecGrant{{Path: `C:\deleted`, SID: "S-1-15-3-1024-a", ID: "1:00"}},
		recordingRevoker(&revoked, nil), func(string) bool { return false })

	if out.Unaccounted() != 0 || out.Failed != 0 || out.Gone != 1 {
		t.Fatalf("%+v, want Gone 1 and nothing unaccounted for: the permission went with the folder", out)
	}
	if len(revoked) != 0 {
		t.Errorf("tried to withdraw from a folder that is gone: %v", revoked)
	}
}

func TestAGrantedFolderThatWasRenamedIsWithdrawnWhereItIsNow(t *testing.T) {
	withLocator(t, func(readExecGrant) (string, directoryLocation) { return `C:\renamed`, locationHere })
	var revoked []string
	out := revokeAllReadExecGrantsWithin(
		[]readExecGrant{{Path: `C:\granted`, SID: "S-1-15-3-1024-a", ID: "1:00"}},
		recordingRevoker(&revoked, nil), func(p string) bool { return p == `C:\renamed` })

	if out.Revoked != 1 || out.Unaccounted() != 0 {
		t.Fatalf("%+v, want the permission withdrawn at the new name", out)
	}
	if len(revoked) != 1 || !strings.HasSuffix(revoked[0], `C:\renamed`) {
		t.Errorf("withdrew from %v, want the folder's new path", revoked)
	}
}

// A folder that is where it was, and one nobody could locate, behave as before.
func TestAGrantWithoutAnAnswerFromTheIDGoesByItsPath(t *testing.T) {
	withLocator(t, func(readExecGrant) (string, directoryLocation) { return "", locationUnknown })
	var revoked []string
	grants := []readExecGrant{
		{Path: `C:\vanished`, SID: "S-1-15-3-1024-a", ID: "1:00"}, // the ID could not be followed
		{Path: `C:\legacy`, SID: "S-1-15-3-1024-a"},               // recorded before IDs were kept
	}
	out := revokeAllReadExecGrantsWithin(grants, recordingRevoker(&revoked, nil), func(string) bool { return false })
	if out.Stranded != 2 || out.Gone != 0 {
		t.Fatalf("%+v, want both stranded: nothing says either folder was deleted", out)
	}

	withLocator(t, func(g readExecGrant) (string, directoryLocation) { return g.Path, locationHere })
	revoked = nil
	out = revokeAllReadExecGrantsWithin(grants[:1], recordingRevoker(&revoked, nil), func(string) bool { return true })
	if out.Revoked != 1 || len(revoked) != 1 || !strings.HasSuffix(revoked[0], `C:\vanished`) {
		t.Errorf("%+v %v, want the folder withdrawn at its recorded path", out, revoked)
	}
}

// A record that already has an ID keeps it, and one without gets it when the
// folder is there to give one.
func TestRecordingAGrantKeepsOrGainsTheFolderID(t *testing.T) {
	dir := tempDir(t)
	got := recordReadExecGrant(nil, "S-1-15-3-1024-a", dir)
	if len(got) != 1 || got[0].Path == "" {
		t.Fatalf("recorded %+v", got)
	}
	again := recordReadExecGrant(got, "S-1-15-3-1024-a", dir)
	if len(again) != 1 || again[0].ID != got[0].ID {
		t.Errorf("a second recording changed the record: %+v then %+v", got, again)
	}

	legacy := []readExecGrant{{Path: dir, SID: "S-1-15-3-1024-a"}}
	filled := recordReadExecGrant(legacy, "S-1-15-3-1024-a", dir)
	if legacy[0].ID != "" {
		t.Error("the caller's slice was changed in place")
	}
	haveID := directoryIdentity(dir) != ""
	if haveID && filled[0].ID == "" {
		t.Error("a record from before IDs were kept did not gain one while its folder was here")
	}
	if gainedIdentity(legacy, filled) != haveID {
		t.Errorf("gainedIdentity disagrees with whether an ID was filled: %+v", filled)
	}
}
