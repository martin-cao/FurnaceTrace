package protocol

import "time"

type User struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
}
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type RegisterRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"displayName"`
}
type SessionResponse struct {
	User      User      `json:"user"`
	ExpiresAt time.Time `json:"expiresAt"`
}
type PairRequest struct {
	Code string `json:"code"`
}
type NotificationPreferences struct {
	Enabled    bool     `json:"enabled"`
	FurnaceIDs []string `json:"furnaceIds"`
	Categories []string `json:"categories"`
	Recoveries bool     `json:"recoveries"`
}
type TelegramBinding struct {
	DisplayName string    `json:"displayName"`
	PairedAt    time.Time `json:"pairedAt"`
}
type TelegramState struct {
	BotUsername   string                  `json:"botUsername"`
	BotConfigured bool                    `json:"botConfigured"`
	BotOnline     bool                    `json:"botOnline"`
	Binding       *TelegramBinding        `json:"binding"`
	Preferences   NotificationPreferences `json:"preferences"`
}

type ProfileUpdate struct {
	Username        string `json:"username"`
	DisplayName     string `json:"displayName"`
	CurrentPassword string `json:"currentPassword"`
}
type PasswordChange struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}
type UserAccessUpdate struct {
	Role    string `json:"role"`
	Enabled *bool  `json:"enabled"`
}
type ManagedUser struct {
	User
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
}
type AccountAccess struct {
	UserID  string `json:"userId"`
	Enabled bool   `json:"enabled"`
	Version int64  `json:"version"`
}
type CameraConfigUpdate struct {
	Mode    string  `json:"mode"`
	RTSPURL *string `json:"rtspUrl,omitempty"`
}
type CameraConfig struct {
	RTSPURL      string `json:"rtspUrl"`
	FurnaceID    string `json:"furnaceId"`
	Mode         string `json:"mode"`
	SourceHost   string `json:"sourceHost"`
	HasSavedRTSP bool   `json:"hasSavedRtsp"`
	Revision     int64  `json:"revision"`
	Applied      bool   `json:"applied"`
	Online       bool   `json:"online"`
	Message      string `json:"message"`
}
