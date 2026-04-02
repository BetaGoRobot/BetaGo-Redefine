package handlers

import "testing"

func TestClampWordCloudOverviewArgs(t *testing.T) {
	got := clampWordCloudOverviewArgs(WordCloudArgs{
		MessageTop: 20,
		ChunkTop:   10,
	})
	if got.MessageTop != wordCloudOverviewMessageTop {
		t.Fatalf("MessageTop = %d, want %d", got.MessageTop, wordCloudOverviewMessageTop)
	}
	if got.ChunkTop != wordCloudOverviewChunkTop {
		t.Fatalf("ChunkTop = %d, want %d", got.ChunkTop, wordCloudOverviewChunkTop)
	}
}

func TestClampWordCloudOverviewArgsKeepsSmallerValues(t *testing.T) {
	got := clampWordCloudOverviewArgs(WordCloudArgs{
		MessageTop: 3,
		ChunkTop:   2,
	})
	if got.MessageTop != 3 {
		t.Fatalf("MessageTop = %d, want 3", got.MessageTop)
	}
	if got.ChunkTop != 2 {
		t.Fatalf("ChunkTop = %d, want 2", got.ChunkTop)
	}
}
