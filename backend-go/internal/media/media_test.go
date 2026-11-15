package media

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// makePNG 生成一张纯色 PNG，用作解码源。
func makePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("生成测试图失败：%v", err)
	}
	return buffer.Bytes()
}

func TestProcessGeneratesTargetWidths(t *testing.T) {
	data := makePNG(t, 2000, 1000)
	result, err := Process(data)
	if err != nil {
		t.Fatalf("Process 失败：%v", err)
	}
	if result.Width != 2000 || result.Height != 1000 {
		t.Fatalf("原图尺寸应如实返回，得到 %dx%d", result.Width, result.Height)
	}
	if len(result.Renditions) != 3 {
		t.Fatalf("2000 宽的图应生成 3 档，得到 %d 档", len(result.Renditions))
	}
	want := []string{"w1600", "w960", "w480"}
	for i, rendition := range result.Renditions {
		if rendition.Name != want[i] {
			t.Fatalf("第 %d 档应为 %s，得到 %s", i, want[i], rendition.Name)
		}
		if rendition.Width != TargetWidths[i] {
			t.Fatalf("%s 宽度应为 %d，得到 %d", rendition.Name, TargetWidths[i], rendition.Width)
		}
		// 等比：2000x1000 缩到 1600 宽，高应为 800。
		if wantHeight := TargetWidths[i] / 2; rendition.Height != wantHeight {
			t.Fatalf("%s 高度应等比缩放为 %d，得到 %d", rendition.Name, wantHeight, rendition.Height)
		}
		if len(rendition.Data) == 0 {
			t.Fatalf("%s 没有产出内容", rendition.Name)
		}
		if !bytes.HasPrefix(rendition.Data, []byte("RIFF")) {
			t.Fatalf("%s 不是 webp", rendition.Name)
		}
	}
}

func TestProcessSkipsLargerThanSource(t *testing.T) {
	result, err := Process(makePNG(t, 700, 400))
	if err != nil {
		t.Fatalf("Process 失败：%v", err)
	}
	if len(result.Renditions) != 1 || result.Renditions[0].Name != "w480" {
		t.Fatalf("700 宽的图只应生成 w480，得到 %+v", result.Renditions)
	}
}

func TestProcessSmallImageHasNoRenditions(t *testing.T) {
	result, err := Process(makePNG(t, 300, 200))
	if err != nil {
		t.Fatalf("Process 失败：%v", err)
	}
	if len(result.Renditions) != 0 {
		t.Fatalf("比最小档还小的图不应生成派生图，得到 %d 档", len(result.Renditions))
	}
	if result.Width != 300 || result.Height != 200 {
		t.Fatalf("尺寸应为 300x200，得到 %dx%d", result.Width, result.Height)
	}
}

func TestProcessRejectsGarbage(t *testing.T) {
	if _, err := Process([]byte("这根本不是图片")); err != ErrUndecodable {
		t.Fatalf("非图片内容应返回 ErrUndecodable，得到 %v", err)
	}
}

func TestProcessable(t *testing.T) {
	cases := map[string]bool{
		"image/jpeg":      true,
		"image/png":       true,
		"image/webp":      true,
		"image/gif":       false, // 动图转静态会丢动画
		"video/mp4":       false,
		"application/pdf": false,
	}
	for contentType, want := range cases {
		if got := Processable(contentType); got != want {
			t.Errorf("Processable(%q) = %v，期望 %v", contentType, got, want)
		}
	}
}
