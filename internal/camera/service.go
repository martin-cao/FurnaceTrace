package camera

import (
	"context"
	"furnace.local/iot/internal/platform"
	"furnace.local/iot/internal/protocol"
	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
	"image"
	"image/color"
	"image/draw"
	"net/http"
	"os/exec"
	"sync"
	"time"
)

func Render(codes []string) (*image.Gray, error) {
	img := image.NewGray(image.Rect(0, 0, 1280, 720))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.Gray{Y: 210}), image.Point{}, draw.Src)
	for i, code := range codes {
		matrix, err := qrcode.NewQRCodeWriter().Encode(code, gozxing.BarcodeFormat_QR_CODE, 400, 400, nil)
		if err != nil {
			return nil, err
		}
		x := 440
		if len(codes) > 1 {
			x = 170 + i*540
		}
		for yy := 0; yy < 400; yy++ {
			for xx := 0; xx < 400; xx++ {
				v := uint8(255)
				if matrix.Get(xx, yy) {
					v = 0
				}
				img.SetGray(x+xx, 160+yy, color.Gray{Y: v})
			}
		}
	}
	return img, nil
}
func Run(ctx context.Context) error {
	var mu sync.RWMutex
	scene := protocol.CameraScene{Codes: []string{}}
	frame, _ := Render(scene.Codes)
	go func() {
		for ctx.Err() == nil {
			cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-re", "-f", "rawvideo", "-pixel_format", "gray", "-video_size", "1280x720", "-framerate", "10", "-i", "pipe:0", "-an", "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-pix_fmt", "yuv420p", "-g", "10", "-rtsp_transport", "tcp", "-f", "rtsp", platform.MediaRTSPURL("rtsp://mediamtx:8554/f1"))
			pipe, err := cmd.StdinPipe()
			if err == nil {
				err = cmd.Start()
			}
			if err == nil {
				for ctx.Err() == nil {
					mu.RLock()
					pixels := frame.Pix
					paused := scene.Paused
					mu.RUnlock()
					if paused {
						select {
						case <-ctx.Done():
						case <-time.After(100 * time.Millisecond):
						}
						continue
					}
					if _, err = pipe.Write(pixels); err != nil {
						break
					}
				}
				_ = pipe.Close()
				_ = cmd.Wait()
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}()
	mux := platform.Mux("camera-sim", nil)
	mux.HandleFunc("PUT /internal/v1/sim/scene", func(w http.ResponseWriter, r *http.Request) {
		var input protocol.CameraScene
		if platform.Read(r, &input) != nil || input.Codes == nil || len(input.Codes) > 2 {
			platform.Error(w, 400, "INVALID_SCENE", "需要 codes 数组，最多两个二维码")
			return
		}
		for _, c := range input.Codes {
			if len(c) == 0 || len(c) > 200 {
				platform.Error(w, 400, "INVALID_SCENE", "二维码内容长度无效")
				return
			}
		}
		img, err := Render(input.Codes)
		if err != nil {
			platform.Error(w, 400, "INVALID_SCENE", "无法生成二维码")
			return
		}
		mu.Lock()
		scene = input
		frame = img
		mu.Unlock()
		w.WriteHeader(204)
	})
	return platform.Serve(ctx, "camera-sim", mux)
}
