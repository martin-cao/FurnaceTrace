package platform

import (
	"net/url"
	"os"
)

// Service credentials are injected only for the internal MediaMTX connection.
func MediaRTSPURL(fallback string) string {
	raw := Env("RTSP_URL", fallback)
	if Env("MEDIA_AUTH_ENABLED", "false") != "true" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.User = url.UserPassword("media", os.Getenv("SERVICE_TOKEN"))
	return u.String()
}
