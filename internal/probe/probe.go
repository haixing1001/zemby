// Package probe 调用 ffprobe 提取媒体信息。
package probe

import (
        "context"
        "encoding/json"
        "fmt"
        "os/exec"
        "strconv"
        "strings"
        "time"

        "go-emby/internal/logx"
        "go-emby/internal/models"
)

// Result ffprobe 输出。
type Result struct {
        Streams []Stream `json:"streams"`
        Format  Format   `json:"format"`
}

// FlexInt 兼容字符串/数字/"N/A" 的整数。
type FlexInt int

// UnmarshalJSON 宽松解析。
func (f *FlexInt) UnmarshalJSON(b []byte) error {
        s := strings.Trim(strings.TrimSpace(string(b)), "\"")
        if s == "" || s == "null" || s == "N/A" {
                *f = 0
                return nil
        }
        n, err := strconv.Atoi(s)
        if err != nil {
                fv, ferr := strconv.ParseFloat(s, 64)
                if ferr != nil {
                        *f = 0
                        return nil
                }
                *f = FlexInt(fv)
                return nil
        }
        *f = FlexInt(n)
        return nil
}

// Stream 流。
type Stream struct {
        Index         int    `json:"index"`
        CodecName     string `json:"codec_name"`
        CodecLongName string `json:"codec_long_name"`
        CodecType     string `json:"codec_type"`
        Profile       string `json:"profile"`
        Level         int    `json:"level"`
        Width         int    `json:"width"`
        Height        int    `json:"height"`
        SampleAspect  string `json:"sample_aspect_ratio"`
        DisplayAspect string `json:"display_aspect_ratio"`
        PixFmt        string `json:"pix_fmt"`
        BitDepth      FlexInt `json:"bits_per_raw_sample"`
        // 色彩
        ColorTransfer   string `json:"color_transfer"`
        ColorPrimaries  string `json:"color_primaries"`
        ColorSpace      string `json:"color_space"`
        // 音频
        Channels   FlexInt `json:"channels"`
        ChannelLay string   `json:"channel_layout"`
        SampleRate FlexInt `json:"sample_rate"`
        // 通用
        Tags struct {
                Language    string `json:"language"`
                Title       string `json:"title"`
        } `json:"tags"`
        AvgFrameRate  string `json:"avg_frame_rate"`
        RFrameRate    string `json:"r_frame_rate"`
        BitRate       string `json:"bit_rate"`
        Disposition   struct {
                Default int `json:"default"`
                Forced  int `json:"forced"`
        } `json:"disposition"`
}

// Format 容器信息。
type Format struct {
        Filename   string `json:"filename"`
        FormatName string `json:"format_name"`
        Duration   string `json:"duration"`
        Size       string `json:"size"`
        BitRate    string `json:"bit_rate"`
}

// HasFFprobe 检测 ffprobe 是否可用。
func HasFFprobe() bool {
        p, err := exec.LookPath("ffprobe")
        return err == nil && p != ""
}

// Probe 提取媒体信息。
func Probe(ctx context.Context, path string) (*Result, error) {
        bin, err := exec.LookPath("ffprobe")
        if err != nil {
                return nil, fmt.Errorf("ffprobe 未安装")
        }
        cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
        defer cancel()
        args := []string{"-v", "error", "-print_format", "json", "-show_format", "-show_streams", path}
        out, err := exec.CommandContext(cctx, bin, args...).Output()
        if err != nil {
                if cctx.Err() == context.DeadlineExceeded {
                        return nil, fmt.Errorf("ffprobe 超时")
                }
                return nil, fmt.Errorf("ffprobe: %v", err)
        }
        var r Result
        if err := json.Unmarshal(out, &r); err != nil {
                return nil, err
        }
        return &r, nil
}

// ToStreams 把 ffprobe 结果转换为 MediaStream 记录。
func ToStreams(sourceID, itemID string, r *Result) []models.MediaStream {
        var out []models.MediaStream
        subIdx := 0
        for _, s := range r.Streams {
                ms := models.MediaStream{
                        SourceID: sourceID, ItemID: itemID, Index: s.Index,
                }
                switch s.CodecType {
                case "video":
                        ms.Type = "Video"
                        ms.Codec = normalizeCodec(s.CodecName)
                        ms.Width, ms.Height = s.Width, s.Height
                        ms.Aspect = aspect(s.Width, s.Height, s.DisplayAspect)
                        ms.PixelFormat = s.PixFmt
                        ms.Profile = s.Profile
                        ms.VideoRange = rangeOf(s)
                        ms.FrameRate = fps(s.AvgFrameRate)
                        ms.BitDepth = int(s.BitDepth)
                        if v, err := strconv.Atoi(s.BitRate); err == nil {
                                ms.BitRate = v
                        }
                        ms.DisplayTitle = fmt.Sprintf("%dp %s", s.Height, strings.ToUpper(ms.Codec))
                        if ms.VideoRange == "HDR" {
                                ms.DisplayTitle += " HDR"
                        }
                case "audio":
                        ms.Type = "Audio"
                        ms.Codec = normalizeCodec(s.CodecName)
                        ms.Language = lang(s.Tags.Language)
                        ms.Channels = int(s.Channels)
                        if s.SampleRate > 0 {
                                ms.SampleRate = int(s.SampleRate)
                        }
                        if v, err := strconv.Atoi(s.BitRate); err == nil {
                                ms.BitRate = v
                        }
                        ms.DisplayTitle = audioTitle(s)
                case "subtitle":
                        ms.Type = "Subtitle"
                        ms.Codec = normalizeCodec(s.CodecName)
                        ms.Language = lang(s.Tags.Language)
                        ms.Title = s.Tags.Title
                        ms.IsDefault = s.Disposition.Default != 0
                        ms.IsForced = s.Disposition.Forced != 0
                        ms.DisplayTitle = subTitle(s, subIdx)
                        subIdx++
                default:
                        continue
                }
                ms.IsDefault = s.Disposition.Default != 0
                out = append(out, ms)
        }
        return out
}

func aspect(w, h int, dar string) string {
        if dar != "" && dar != "0:1" && dar != "N/A" {
                return dar
        }
        if w == 0 || h == 0 {
                return ""
        }
        if w*9 == h*16 { return "16:9" }
        if w*3 == h*4 { return "4:3" }
        if w*2 == h*1 { return "2.00:1" }
        if w*3 == h*1 { return "3:1" }
        if w == h { return "1:1" }
        return fmt.Sprintf("%.2f:1", float64(w)/float64(h))
}

func rangeOf(s Stream) string {
        ct := strings.ToLower(s.ColorTransfer)
        if strings.Contains(ct, "smpte2084") || strings.Contains(ct, "arib") || strings.Contains(ct, "hlg") {
                return "HDR"
        }
        if strings.Contains(strings.ToLower(s.CodecName), "dovi") {
                return "HDR"
        }
        return "SDR"
}

func fps(v string) float64 {
        parts := strings.Split(v, "/")
        if len(parts) == 2 {
                a, e1 := strconv.ParseFloat(parts[0], 64)
                b, e2 := strconv.ParseFloat(parts[1], 64)
                if e1 == nil && e2 == nil && b != 0 {
                        return float64(int(a/b*100)) / 100
                }
        }
        f, _ := strconv.ParseFloat(v, 64)
        return f
}

func normalizeCodec(c string) string {
        switch strings.ToLower(c) {
        case "h264": return "h264"
        case "hevc", "h265": return "hevc"
        case "mpeg2video": return "mpeg2video"
        case "vc1": return "vc1"
        case "aac": return "aac"
        case "ac3", "eac3": return strings.ToLower(c)
        case "truehd": return "truehd"
        case "dts": return "dts"
        case "subrip": return "srt"
        case "ass", "ssa": return strings.ToLower(c)
        case "hdmv_pgs_subtitle": return "pgs"
        case "dvd_subtitle": return "vobsub"
        }
        return strings.ToLower(c)
}

func lang(l string) string {
        switch strings.ToLower(l) {
        case "chi", "zho": return "chi"
        case "eng": return "eng"
        case "jpn": return "jpn"
        case "kor": return "kor"
        case "und", "": return "und"
        }
        return strings.ToLower(l)
}

func audioTitle(s Stream) string {
        langName := map[string]string{"chi": "中文", "eng": "英语", "jpn": "日语", "kor": "韩语", "und": "未知"}[lang(s.Tags.Language)]
        if langName == "" {
                langName = s.Tags.Language
        }
        ch := ""
        switch s.Channels {
        case 1: ch = "1.0"
        case 2: ch = "2.0"
        case 6: ch = "5.1"
        case 8: ch = "7.1"
        default:
                if s.Channels > 0 {
                        ch = fmt.Sprintf("%d.0", s.Channels)
                }
        }
        title := fmt.Sprintf("%s %s %s", strings.ToUpper(s.CodecName), ch, langName)
        if s.Tags.Title != "" {
                title = s.Tags.Title
        }
        if s.Disposition.Default != 0 {
                title += "（默认）"
        }
        return title
}

func subTitle(s Stream, idx int) string {
        langName := map[string]string{"chi": "中文", "eng": "英语", "jpn": "日语", "kor": "韩语", "und": "未知"}[lang(s.Tags.Language)]
        if langName == "" {
                langName = s.Tags.Language
        }
        t := fmt.Sprintf("字幕 %d (%s)", idx+1, strings.ToUpper(s.CodecName))
        if s.Tags.Title != "" {
                t = s.Tags.Title
        } else if langName != "" {
                t = fmt.Sprintf("%s (%s)", langName, strings.ToUpper(s.CodecName))
        }
        return t
}

// RunTimeTicks 计算总时长 ticks。
func RunTimeTicks(r *Result) int64 {
        d, err := strconv.ParseFloat(r.Format.Duration, 64)
        if err != nil || d <= 0 {
                // 尝试视频流 duration？ffprobe -show_streams 有 duration 字段但我们未映射
                return 0
        }
        return int64(d * 10000000)
}

// LogScan 记录 probe 日志。
func LogScan(path string) { logx.InfoC(logx.CatProbe, "ffprobe 分析 %s", path) }
