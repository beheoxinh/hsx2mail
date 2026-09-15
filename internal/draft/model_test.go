package draft

import "testing"

func TestIsSynced(t *testing.T) {
	tests := []struct {
		name       string
		syncStatus SyncStatus
		uid        uint32
		want       bool
	}{
		{
			name:       "synced with UID",
			syncStatus: SyncStatusSynced,
			uid:        42,
			want:       true,
		},
		{
			name:       "synced without UID",
			syncStatus: SyncStatusSynced,
			uid:        0,
			want:       false,
		},
		{
			name:       "pending with UID",
			syncStatus: SyncStatusPending,
			uid:        42,
			want:       false,
		},
		{
			name:       "failed with UID",
			syncStatus: SyncStatusFailed,
			uid:        42,
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &Draft{
				SyncStatus: tt.syncStatus,
				IMAPUID:    tt.uid,
			}
			got := d.IsSynced()
			if got != tt.want {
				t.Errorf("IsSynced() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNeedsSync(t *testing.T) {
	tests := []struct {
		name       string
		syncStatus SyncStatus
		want       bool
	}{
		{
			name:       "pending needs sync",
			syncStatus: SyncStatusPending,
			want:       true,
		},
		{
			name:       "failed needs sync",
			syncStatus: SyncStatusFailed,
			want:       true,
		},
		{
			name:       "synced does not need sync",
			syncStatus: SyncStatusSynced,
			want:       false,
		},
		{
			name:       "empty string does not need sync",
			syncStatus: "",
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &Draft{
				SyncStatus: tt.syncStatus,
			}
			got := d.NeedsSync()
			if got != tt.want {
				t.Errorf("NeedsSync() = %v, want %v", got, tt.want)
			}
		})
	}
}
