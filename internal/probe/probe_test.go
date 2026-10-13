package probe

import (
	"strings"
	"testing"
)

// TestProbeArgsRemoteParams ffprobe 参数构造：远程直链加网络超时与 UA，本地文件不加。
func TestProbeArgsRemoteParams(t *testing.T) {
	local := probeArgs("/media/movie.mkv")
	if len(local) != 7 || local[len(local)-1] != "/media/movie.mkv" {
		t.Fatalf("本地路径参数错误: %v", local)
	}
	for _, a := range local {
		if strings.Contains(a, "rw_timeout") || strings.Contains(a, "user_agent") {
			t.Fatalf("本地路径不应含网络参数: %v", local)
		}
	}

	for _, url := range []string{"https://example.com/v.mp4", "http://example.com/v.mkv"} {
		args := probeArgs(url)
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "-rw_timeout 30000000") {
			t.Fatalf("远程直链应有 30s 网络超时: %v", args)
		}
		if !strings.Contains(joined, "-user_agent "+probeUserAgent) {
			t.Fatalf("远程直链应有浏览器 UA: %v", args)
		}
		if args[len(args)-1] != url {
			t.Fatalf("URL 应为最后参数: %v", args)
		}
	}

	if !IsRemoteSource("https://x") || !IsRemoteSource("http://x") || IsRemoteSource("/local.mkv") {
		t.Fatal("IsRemoteSource 判定错误")
	}
}
