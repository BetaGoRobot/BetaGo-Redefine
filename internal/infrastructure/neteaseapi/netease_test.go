package neteaseapi

import "testing"

func TestJoinSongIDs(t *testing.T) {
	t.Parallel()

	got := joinSongIDs([]int{0, 123, 456, 0, 789})
	if got != "123,456,789" {
		t.Fatalf("joinSongIDs() = %q, want %q", got, "123,456,789")
	}
}

func TestMergeLyrics(t *testing.T) {
	t.Parallel()

	lyrics := "[00:01.00]line1\n[00:02.00]line2"
	translated := "[00:01.00]trans1\n[00:02.00]trans2"

	got := mergeLyrics(lyrics, translated)
	want := "line1\ntrans1\n\nline2\ntrans2\n\n"
	if got != want {
		t.Fatalf("mergeLyrics() = %q, want %q", got, want)
	}
}
