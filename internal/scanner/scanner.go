// Package scanner 扫描引擎：全量扫描 / 增量刷新 / 目录遍历。
package scanner

import (
        "fmt"
        "os"
        "path/filepath"
        "sort"
        "strings"
        "sync"
        "time"

        "gorm.io/gorm"

        "go-emby/internal/config"
        "go-emby/internal/db"
        "go-emby/internal/logx"
        "go-emby/internal/models"
        "go-emby/internal/nfo"
        "go-emby/internal/probe"
)

// Scanner 扫描器。
type Scanner struct {
        cfg *config.Config

        mu       sync.Mutex
        running  map[string]bool // libraryID -> scanning
        stopFlag map[string]bool // libraryID -> requested stop

        // 探测/刮削队列
        probeQueue  chan probeTask
        scrapeQueue chan scrapeTask
        taskWG      sync.WaitGroup
}

type probeTask struct {
        sourceID string
        itemID   string
        path     string
        mtime    int64
}

type scrapeTask struct {
        itemID string
}

// New 创建扫描器并启动工作线程。
func New(cfg *config.Config) *Scanner {
        s := &Scanner{
                cfg:         cfg,
                running:     map[string]bool{},
                stopFlag:    map[string]bool{},
                probeQueue:  make(chan probeTask, 4096),
                scrapeQueue: make(chan scrapeTask, 4096),
        }
        dataDirOnce.Do(func() { dataDirValue = cfg.DataDir })
        s.StartWorkers()
        // 启动时恢复实时监控状态
        if LoadScrapeConfig().Realtime {
                s.SetRealtime(true)
        }
        return s
}

// IsScanning 是否正在扫描某库。
func (s *Scanner) IsScanning(libID string) bool {
        s.mu.Lock()
        defer s.mu.Unlock()
        return s.running[libID]
}

// RequestStop 请求停止扫描。
func (s *Scanner) RequestStop(libID string) {
        s.mu.Lock()
        defer s.mu.Unlock()
        s.stopFlag[libID] = true
}

func (s *Scanner) stopped(libID string) bool {
        s.mu.Lock()
        defer s.mu.Unlock()
        return s.stopFlag[libID]
}

// ScanLibrary 扫描媒体库（异步）。mode: full=全量 update=增量
func (s *Scanner) ScanLibrary(libID, mode string) error {
        var lib models.Library
        if err := db.DB.First(&lib, "id = ?", libID).Error; err != nil {
                return fmt.Errorf("媒体库不存在")
        }
        s.mu.Lock()
        if s.running[libID] {
                s.mu.Unlock()
                return fmt.Errorf("该媒体库正在扫描中")
        }
        s.running[libID] = true
        s.stopFlag[libID] = false
        s.mu.Unlock()

        go func() {
                defer func() {
                        s.mu.Lock()
                        delete(s.running, libID)
                        s.mu.Unlock()
                }()
                s.runScan(&lib, mode)
        }()
        return nil
}

// seenPaths 记录扫描中看到的文件。
type seenPaths map[string]int64 // path -> mtime

func (s *Scanner) runScan(lib *models.Library, mode string) {
        start := time.Now()
        full := mode != "update"
        logx.Scan("开始%s媒体库「%s」(%s)", map[bool]string{true: "全量", false: "增量"}[full], lib.Name, mode)

        task := models.ScanTask{LibraryID: lib.ID, Mode: mode, State: "running", StartedAt: time.Now()}
        db.DB.Create(&task)
        defer func() {
                now := time.Now()
                task.State, task.EndedAt = "done", &now
                db.DB.Save(&task)
                db.DB.Model(&models.Library{}).Where("id = ?", lib.ID).Update("last_scan", now)
        }()

        db.DB.Model(&models.Library{}).Where("id = ?", lib.ID).Update("scanning", true)
        defer db.DB.Model(&models.Library{}).Where("id = ?", lib.ID).Update("scanning", false)

        seen := seenPaths{}
        var counters struct{ files, newItems, updated, skipped int }

        for _, root := range strings.Split(lib.Path, ";") {
                root = strings.TrimSpace(root)
                if root == "" {
                        continue
                }
                if _, err := os.Stat(root); err != nil {
                        logx.Warn("媒体目录不可访问: %s", root)
                        continue
                }
                if lib.Type == "tvshows" {
                        s.scanTVDir(lib, root, full, seen, &counters)
                } else {
                        s.scanMoviesDir(lib, root, full, seen, &counters)
                }
                if s.stopped(lib.ID) {
                        logx.Warn("媒体库「%s」扫描被手动停止", lib.Name)
                        break
                }
        }

        // 删除已不存在的条目（文件消失）
        var removed int
        if full || true { // 增量也校验存在性（开销低）
                removed = s.removeMissing(lib.ID, seen)
        }

        logx.Scan("媒体库「%s」扫描完成：文件 %d，新增 %d，更新 %d，跳过 %d，清理 %d，耗时 %s",
                lib.Name, counters.files, counters.newItems, counters.updated, counters.skipped, removed, time.Since(start).Round(time.Second))
}

// walkFile 收集一个视频文件并处理。
type videoFile struct {
        path  string
        mtime int64
        size  int64
}

func listVideos(root string) []videoFile {
        var out []videoFile
        _ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
                if err != nil {
                        return nil
                }
                name := d.Name()
                if d.IsDir() {
                        if skipDirs[name] || strings.HasPrefix(name, ".") {
                                return filepath.SkipDir
                        }
                        return nil
                }
                if !IsVideo(name) {
                        return nil
                }
                info, err := d.Info()
                if err != nil {
                        return nil
                }
                out = append(out, videoFile{path: p, mtime: info.ModTime().Unix(), size: info.Size()})
                return nil
        })
        sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
        return out
}

func (s *Scanner) scanMoviesDir(lib *models.Library, root string, full bool, seen seenPaths, cnt *struct{ files, newItems, updated, skipped int }) {
        for _, vf := range listVideos(root) {
                if s.stopped(lib.ID) {
                        return
                }
                seen[vf.path] = vf.mtime
                cnt.files++
                // 增量：未变化跳过重建，但仍刷新字幕与本地图片（轻量）
                if !full {
                        var ex models.Item
                        if err := db.DB.Where("library_id = ? AND path = ? AND mtime = ?", lib.ID, vf.path, vf.mtime).First(&ex).Error; err == nil {
                                cnt.skipped++
                                s.refreshLight(&ex, vf)
                                continue
                        }
                }
                if err := s.upsertMovie(lib, vf); err != nil {
                        logx.Warn("处理失败 %s: %v", vf.path, err)
                        continue
                }
                cnt.updated++
        }
}

func (s *Scanner) scanTVDir(lib *models.Library, root string, full bool, seen seenPaths, cnt *struct{ files, newItems, updated, skipped int }) {
        // 第一遍：收集全部视频并按剧集目录分组
        groups := map[string][]videoFile{} // seriesDir -> files
        var order []string
        for _, vf := range listVideos(root) {
                if s.stopped(lib.ID) {
                        return
                }
                sd := FindSeriesDir(vf.path, root)
                if _, ok := groups[sd]; !ok {
                        order = append(order, sd)
                }
                groups[sd] = append(groups[sd], vf)
        }
        for _, sd := range order {
                if s.stopped(lib.ID) {
                        return
                }
                s.upsertSeries(lib, sd, groups[sd], full, seen, cnt)
        }
}

// upsertMovie 建立或更新电影条目。
func (s *Scanner) upsertMovie(lib *models.Library, vf videoFile) error {
        parsed := ParseName(filepath.Base(vf.path))
        item := models.Item{
                LibraryID: lib.ID, Type: "Movie",
                Name: parsed.Title, SortName: strings.ToLower(parsed.Title),
                Year: parsed.Year, Path: vf.path, Mtime: vf.mtime, Size: vf.size,
                DateCreated: time.Now(),
        }
        existing, err := findItem(lib.ID, vf.path)
        if err == nil && existing != nil {
                item.ID = existing.ID
                item.Scraped = existing.Scraped
                item.Poster, item.Backdrop, item.Thumb, item.Logo = existing.Poster, existing.Backdrop, existing.Thumb, existing.Logo
                item.ProviderIDs, item.Overview = existing.ProviderIDs, existing.Overview
                item.Name, item.OriginalTitle = existing.Name, existing.OriginalTitle
                item.Genres, item.Studios, item.People, item.Tags = existing.Genres, existing.Studios, existing.People, existing.Tags
                item.PremiereDate, item.CommunityRating, item.OfficialRating, item.RunTimeTicks =
                        existing.PremiereDate, existing.CommunityRating, existing.OfficialRating, existing.RunTimeTicks
        }
        if item.ID == "" {
                item.ID = models.NewID()
        }
        if item.SortName == "" {
                item.SortName = strings.ToLower(item.Name)
        }
        item.ImageRev = imageRev(item.Poster, fmt.Sprint(vf.mtime))
        if err := db.DB.Save(&item).Error; err != nil {
                return err
        }
        s.restoreMediaInfo(item.ID) // 持久化恢复（命中后不再重新探测）
        // NFO
        s.applyNFO(&item, vf.path, "")
        // 媒体源
        src := s.ensureSource(&item, vf)
        // 字幕
        s.applySubtitles(&item, src, vf.path)
        // 本地图片
        s.applyLocalImages(&item, filepath.Dir(vf.path), strings.TrimSuffix(filepath.Base(vf.path), filepath.Ext(vf.path)))
        // 排队探测与刮削
        if s.needsProbe(&item) {
                s.enqueueProbe(&item, src, vf.mtime)
        }
        if !item.Scraped {
                s.enqueueScrape(item.ID)
        }
        return nil
}

// upsertSeries 处理一个剧集目录。
func (s *Scanner) upsertSeries(lib *models.Library, seriesDir string, files []videoFile, full bool, seen seenPaths, cnt *struct{ files, newItems, updated, skipped int }) {
        // 剧集条目
        var series models.Item
        seriesDirty := false
        existingSeries, _ := findItem(lib.ID, seriesDir)

        seriesNFO := readNFOIn(seriesDir, "tvshow.nfo")
        seriesName := filepath.Base(seriesDir)
        if seriesNFO != nil && seriesNFO.Title != "" {
                seriesName = seriesNFO.Title
        } else {
                p := ParseName(seriesName)
                seriesName = p.Title
                if p.Year > 0 && existingSeries == nil {
                        // 年份保留在刮削阶段
                }
        }
        if existingSeries == nil {
                series = models.Item{ID: models.NewID(), LibraryID: lib.ID, Type: "Series",
                        Name: seriesName, SortName: strings.ToLower(seriesName), Path: seriesDir,
                        Mtime: dirMtime(seriesDir), DateCreated: time.Now()}
                seriesDirty = true
        } else {
                series = *existingSeries
                if series.Name != seriesName {
                        series.Name = seriesName
                        seriesDirty = true
                }
        }
        if seriesDirty {
                series.ImageRev = imageRev(series.Poster, fmt.Sprint(series.Mtime))
                db.DB.Save(&series)
        }

        // tvshow.nfo 元数据
        if seriesNFO != nil {
                applyNFOToSeries(&series, seriesNFO)
                series.Scraped = series.Scraped || series.ProviderIDs != ""
                db.DB.Save(&series)
                s.applyLocalImages(&series, seriesDir, "")
        }

        // 集分组：season -> episodes
        type epInfo struct {
                vf      videoFile
                season  int
                episode int
        }
        var episodes []epInfo
        seasonDirs := map[int]string{}

        for _, vf := range files {
                seen[vf.path] = vf.mtime
                cnt.files++
                p := ParseName(filepath.Base(vf.path))
                if p.IsSample {
                        continue
                }
                season, ep := p.Season, p.Episode
                if season == 0 {
                        // 从目录名推断
                        if n := ParseSeasonDir(filepath.Base(filepath.Dir(vf.path))); n > 0 {
                                season = n
                        } else {
                                season = 1
                        }
                }
                if ep == 0 {
                        logx.Warn("无法识别集号，按补充内容跳过: %s", filepath.Base(vf.path))
                        continue
                }
                episodes = append(episodes, epInfo{vf, season, ep})
                if n := ParseSeasonDir(filepath.Base(filepath.Dir(vf.path))); n == season {
                        seasonDirs[season] = filepath.Dir(vf.path)
                } else if _, ok := seasonDirs[season]; !ok {
                        seasonDirs[season] = ""
                }
        }

        // 季条目
        seasonItems := map[int]*models.Item{}
        for sn, sdir := range seasonDirs {
                sPath := sdir
                if sPath == "" {
                        sPath = series.Path + "#S" + fmt.Sprint(sn)
                }
                exS, _ := findItem(lib.ID, sPath)
                var sItem models.Item
                if exS == nil {
                        sItem = models.Item{ID: models.NewID(), LibraryID: lib.ID, Type: "Season",
                                Name: fmt.Sprintf("第 %d 季", sn), Path: sPath, ParentID: series.ID,
                                SeriesID: series.ID, SeriesName: series.Name, ParentIndexNumber: sn,
                                Mtime: dirMtime(seriesDir), DateCreated: time.Now()}
                        db.DB.Save(&sItem)
                } else {
                        sItem = *exS
                        if sItem.ParentID != series.ID || sItem.SeriesName != series.Name {
                                sItem.ParentID, sItem.SeriesID, sItem.SeriesName, sItem.ParentIndexNumber = series.ID, series.ID, series.Name, sn
                                db.DB.Save(&sItem)
                        }
                }
                // season.nfo / 季海报
                if sdir != "" {
                        if sf := readNFOIn(sdir, "season.nfo"); sf != nil && sf.Title != "" {
                                sItem.Name = sf.Title
                                db.DB.Save(&sItem)
                        }
                        s.applyLocalImages(&sItem, sdir, fmt.Sprintf("season%d", sn))
                }
                seasonItems[sn] = &sItem
                // 季刮削跟随剧集
                if !series.Scraped {
                        _ = sn
                }
        }

        // 集条目
        for _, e := range episodes {
                if !full {
                        var ex models.Item
                        if err := db.DB.Where("library_id = ? AND path = ? AND mtime = ?", lib.ID, e.vf.path, e.vf.mtime).First(&ex).Error; err == nil {
                                cnt.skipped++
                                s.refreshLight(&ex, e.vf)
                                continue
                        }
                }
                p := ParseName(filepath.Base(e.vf.path))
                name := fmt.Sprintf("第 %d 集", e.episode)
                if p.Title != "" && !strings.EqualFold(p.Title, series.Name) {
                        name = p.Title
                }
                item := models.Item{
                        LibraryID: lib.ID, Type: "Episode",
                        Name: name, SortName: fmt.Sprintf("%04d", e.episode),
                        Path: e.vf.path, Mtime: e.vf.mtime, Size: e.vf.size,
                        ParentID: seasonItems[e.season].ID, SeriesID: series.ID, SeriesName: series.Name,
                        SeasonID: seasonItems[e.season].ID,
                        IndexNumber: e.episode, ParentIndexNumber: e.season,
                        DateCreated: time.Now(),
                }
                existing, err := findItem(lib.ID, e.vf.path)
                if err == nil && existing != nil {
                        item.ID = existing.ID
                        item.Scraped = existing.Scraped
                        item.Poster, item.Thumb = existing.Poster, existing.Thumb
                        item.ProviderIDs, item.Overview = existing.ProviderIDs, existing.Overview
                        if existing.Scraped && existing.Name != "" {
                                item.Name = existing.Name
                        }
                        item.RunTimeTicks = existing.RunTimeTicks
                } else {
                        item.ID = models.NewID()
                }
                item.ImageRev = imageRev(item.Thumb, fmt.Sprint(e.vf.mtime))
                if err := db.DB.Save(&item).Error; err != nil {
                        continue
                }
                cnt.updated++
                s.restoreMediaInfo(item.ID)
                s.applyNFO(&item, e.vf.path, "")
                src := s.ensureSource(&item, e.vf)
                s.applySubtitles(&item, src, e.vf.path)
                s.applyLocalImages(&item, filepath.Dir(e.vf.path), strings.TrimSuffix(filepath.Base(e.vf.path), filepath.Ext(e.vf.path)))
                if s.needsProbe(&item) {
                        s.enqueueProbe(&item, src, e.vf.mtime)
                }
        }

        // 剧集刮削
        if !series.Scraped {
                s.enqueueScrape(series.ID)
        }
}

// findItem 按 库+路径 查找条目；找不到返回 nil, false。
func findItem(libID, path string) (*models.Item, error) {
        var it models.Item
        err := db.DB.Where("library_id = ? AND path = ?", libID, path).First(&it).Error
        if err != nil {
                if err == gorm.ErrRecordNotFound {
                        return nil, nil
                }
                return nil, err
        }
        return &it, nil
}

// removeMissing 删除文件已不存在的条目。
func (s *Scanner) removeMissing(libID string, seen seenPaths) int {
        var items []models.Item
        if err := db.DB.Where("library_id = ? AND type IN (?)", libID, []string{"Movie", "Episode"}).Find(&items).Error; err != nil {
                return 0
        }
        removed := 0
        for _, it := range items {
                if _, ok := seen[it.Path]; !ok {
                        if _, err := os.Stat(it.Path); err != nil {
                                s.persistMediaInfo(it.ID) // 持久化开启时保留媒体信息
                                s.deleteItemCascade(it.ID)
                                removed++
                        }
                }
        }
        // 删除没有集的季/没有季的剧集
        var emptySeasons []models.Item
        db.DB.Where("library_id = ? AND type = 'Season'", libID).Find(&emptySeasons)
        for _, sn := range emptySeasons {
                var c int64
                db.DB.Model(&models.Item{}).Where("parent_id = ?", sn.ID).Count(&c)
                if c == 0 {
                        s.deleteItemCascade(sn.ID)
                }
        }
        var emptySeries []models.Item
        db.DB.Where("library_id = ? AND type = 'Series'", libID).Find(&emptySeries)
        for _, sr := range emptySeries {
                var c int64
                db.DB.Model(&models.Item{}).Where("series_id = ?", sr.ID).Count(&c)
                if c == 0 {
                        if _, err := os.Stat(sr.Path); err != nil {
                                s.deleteItemCascade(sr.ID)
                        }
                }
        }
        return removed
}

// deleteItemCascade 删除条目及其子项、源、流。
func (s *Scanner) deleteItemCascade(id string) {
        db.DB.Where("item_id = ?", id).Delete(&models.MediaStream{})
        db.DB.Where("item_id = ?", id).Delete(&models.MediaSource{})
        db.DB.Where("item_id = ?", id).Delete(&models.UserDatum{})
        db.DB.Where("id = ?", id).Delete(&models.Item{})
}

// dirMtime 目录修改时间。
func dirMtime(path string) int64 {
        if fi, err := os.Stat(path); err == nil {
                return fi.ModTime().Unix()
        }
        return 0
}

// imageRev 图片修订号（用于客户端缓存）。
func imageRev(paths ...string) string {
        combined := strings.Join(paths, "|")
        for _, p := range paths {
                if fi, err := os.Stat(p); err == nil {
                        combined += fmt.Sprintf("|%d", fi.ModTime().Unix())
                }
        }
        if len(combined) > 32 {
                combined = combined[:32]
        }
        if combined == "" {
                return "0"
        }
        return fmt.Sprintf("%x", fnvHash(combined))[:12]
}

func fnvHash(s string) uint64 {
        var h uint64 = 14695981039346656037
        for i := 0; i < len(s); i++ {
                h ^= uint64(s[i])
                h *= 1099511628211
        }
        return h
}

// needsProbe 是否需要探测。
func (s *Scanner) needsProbe(item *models.Item) bool {
        if !hasFFprobe {
                return false
        }
        var src models.MediaSource
        if err := db.DB.Where("item_id = ?", item.ID).First(&src).Error; err != nil {
                return false
        }
        if strings.HasPrefix(src.Path, "http://") || strings.HasPrefix(src.Path, "https://") {
                return false // 远程流不探测
        }
        var cnt int64
        db.DB.Model(&models.MediaStream{}).Where("item_id = ?", item.ID).Count(&cnt)
        if cnt == 0 {
                return true
        }
        // 文件变化过则重新探测
        var p models.Item
        if db.DB.Select("mtime").First(&p, "id = ?", item.ID).Error == nil {
                var probed int64
                db.DB.Model(&models.MediaStream{}).Where("item_id = ? AND is_external = ?", item.ID, false).Count(&probed)
                return probed == 0
        }
        return false
}

// hasFFprobe 缓存探测工具可用性。
var hasFFprobe = false

func init() {
        hasFFprobe = probe.HasFFprobe()
}

// readNFOIn 在目录中查找指定 NFO 文件并解析。
func readNFOIn(dir, name string) *nfo.NFO {
        p := filepath.Join(dir, name)
        if fi, err := os.Stat(p); err != nil || fi.IsDir() {
                return nil
        }
        if n, err := nfo.ParseFile(p); err == nil {
                return n
        }
        return nil
}
