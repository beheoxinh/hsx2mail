package notification

import "testing"

func TestNotificationStruct(t *testing.T) {
	tests := []struct {
		name      string
		notif     Notification
		wantTitle string
		wantBody  string
		wantIcon  string
	}{
		{
			name: "all fields populated",
			notif: Notification{
				Title: "New Message",
				Body:  "You have a new email from Alice",
				Icon:  "/path/to/icon.png",
				Data: NotificationData{
					AccountID: "acc-1",
					FolderID:  "folder-inbox",
					ThreadID:  "thread-42",
				},
			},
			wantTitle: "New Message",
			wantBody:  "You have a new email from Alice",
			wantIcon:  "/path/to/icon.png",
		},
		{
			name: "minimal fields",
			notif: Notification{
				Title: "Alert",
				Body:  "Something happened",
			},
			wantTitle: "Alert",
			wantBody:  "Something happened",
			wantIcon:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.notif.Title != tt.wantTitle {
				t.Errorf("Title = %q, want %q", tt.notif.Title, tt.wantTitle)
			}
			if tt.notif.Body != tt.wantBody {
				t.Errorf("Body = %q, want %q", tt.notif.Body, tt.wantBody)
			}
			if tt.notif.Icon != tt.wantIcon {
				t.Errorf("Icon = %q, want %q", tt.notif.Icon, tt.wantIcon)
			}
		})
	}
}

func TestNotificationDataStruct(t *testing.T) {
	tests := []struct {
		name string
		data NotificationData
	}{
		{
			name: "all fields populated",
			data: NotificationData{
				AccountID: "account-abc",
				FolderID:  "folder-inbox",
				ThreadID:  "thread-xyz",
			},
		},
		{
			name: "empty fields",
			data: NotificationData{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := Notification{Data: tt.data}
			if n.Data.AccountID != tt.data.AccountID {
				t.Errorf("AccountID = %q, want %q", n.Data.AccountID, tt.data.AccountID)
			}
			if n.Data.FolderID != tt.data.FolderID {
				t.Errorf("FolderID = %q, want %q", n.Data.FolderID, tt.data.FolderID)
			}
			if n.Data.ThreadID != tt.data.ThreadID {
				t.Errorf("ThreadID = %q, want %q", n.Data.ThreadID, tt.data.ThreadID)
			}
		})
	}
}
