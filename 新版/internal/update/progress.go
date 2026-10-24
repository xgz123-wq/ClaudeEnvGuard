package update

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 进度只从脚本写的文件推断：记录.json 的 Timing（各阶段累计秒数）、Command（马上要弹 UAC）、
// 统一维护\进度.log（UAC 内每步开始 / 完成）、下载中的临时文件大小。不解析屏幕输出。

// Download 是 Desktop 安装包的下载进度。
type Download struct {
	MB      float64 `json:"mb"`
	TotalMB float64 `json:"totalMb"`
	Speed   float64 `json:"speed"` // MB/s，本步平均
}

// observer 记住每一步第一次被看到在跑的时刻，用来给没有计时的时段（npm、等 UAC、UAC 内）配秒表。
type observer struct {
	start time.Time
	seen  map[string]time.Time // 第一次看到在跑
	end   map[string]time.Time // 第一次看到完成
}

func newObserver(now time.Time) *observer {
	return &observer{start: now, seen: map[string]time.Time{}, end: map[string]time.Time{}}
}

// since 返回某步从第一次看到在跑到现在的秒数。
func (o *observer) since(id string, now time.Time) float64 {
	t, ok := o.seen[id]
	if !ok {
		o.seen[id] = now
		return 0
	}
	return now.Sub(t).Seconds()
}

// took 返回某步观察到的用时（开始到第一次看到完成）；没看到它在跑过时为 0。
func (o *observer) took(id string, now time.Time) float64 {
	e, ok := o.end[id]
	if !ok {
		e = now
		o.end[id] = now
	}
	s, ok := o.seen[id]
	if !ok {
		return 0
	}
	return e.Sub(s).Seconds()
}

var lineRe = regexp.MustCompile(`^(开始|完成|失败) (\S+?)(?:（([\d.]+) 秒）)?$`)

// progressLine 是 进度.log 的一行。
type progressLine struct {
	Verb string // 开始 / 完成 / 失败
	Name string
	Secs float64
}

func parseLines(lines []string) []progressLine {
	var out []progressLine
	for _, l := range lines {
		m := lineRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		s, _ := strconv.ParseFloat(m[3], 64)
		out = append(out, progressLine{Verb: m[1], Name: m[2], Secs: s})
	}
	return out
}

// snapshot 是推断结果。Cur 是正在跑（或停住）的步骤下标，-1 表示全部完成。
type snapshot struct {
	Steps    []Step
	Cur      int
	Download *Download
}

// infer 根据文件把计划里每一步标成 done / run / wait，并给出秒数。alive 表示脚本进程还在跑。
func infer(plan []Step, runDir, root string, rec *scriptRecord, lines []string, obs *observer, now time.Time, alive bool) snapshot {
	steps := append([]Step(nil), plan...)
	t := orderedSeconds{Values: map[string]float64{}}
	if rec != nil {
		t = rec.Timing
	}
	pl := parseLines(lines)
	maintDone := map[string]float64{}
	maintFail := map[string]bool{}
	maintStarted := map[string]bool{}
	for _, l := range pl {
		id := maintSteps[l.Name]
		switch l.Verb {
		case "开始":
			maintStarted[id] = true
		case "完成":
			maintDone[id] = l.Secs
		case "失败":
			maintFail[id] = true
			maintDone[id] = l.Secs
		}
	}
	if rec != nil && rec.Maintenance != nil {
		for _, k := range rec.Maintenance.StepSeconds.Keys {
			if id := maintSteps[k]; id != "" {
				if _, ok := maintDone[id]; !ok {
					maintDone[id] = rec.Maintenance.StepSeconds.Values[k]
				}
			}
		}
	}
	prevOf := func(keys ...string) float64 {
		for _, k := range keys {
			if t.Has(k) {
				return t.Values[k]
			}
		}
		return 0
	}
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(runDir, name))
		return err == nil
	}
	var dl *Download
	reused := exists("安装包审核-复用.log") && rec != nil && rec.Desktop != nil && rec.Desktop.Reused

	done := func(i int, secs float64) { steps[i].State, steps[i].Secs = "done", round1(secs) }
	for i := range steps {
		s := &steps[i]
		switch s.ID {
		case "check":
			if t.Has("查版本") {
				done(i, t.Values["查版本"])
			}
		case "cli":
			if t.Has("CLI安装") {
				done(i, t.Values["CLI安装"]-prevOf("查版本"))
			}
		case "plugin":
			if t.Has("插件安装") {
				done(i, t.Values["插件安装"]-prevOf("CLI安装", "查版本"))
			}
		case "download":
			switch {
			case reused || (t.Has("Desktop包就绪") && rec.Desktop != nil && rec.Desktop.Reused):
				done(i, 0)
				s.Detail = "复用已审核过的同版本安装包，不用重下"
			case t.Has("Desktop包就绪") && rec.Desktop != nil:
				done(i, rec.Desktop.DownloadSeconds)
			case exists("安装包审核.log"):
				done(i, obs.took(s.ID, now))
			}
		case "inspect":
			if t.Has("Desktop包就绪") {
				secs := t.Values["Desktop包就绪"] - prevOf("插件安装", "CLI安装", "查版本")
				if rec.Desktop != nil {
					secs -= rec.Desktop.DownloadSeconds
				}
				done(i, secs)
			}
		case "hashes":
			if t.Has("指纹汇总") {
				done(i, t.Values["指纹汇总"]-prevOf("Desktop包就绪", "插件安装", "CLI安装", "查版本"))
			}
		case "uac":
			if len(pl) > 0 || t.Has("一键更新") {
				done(i, obs.took(s.ID, now))
			}
		default:
			if secs, ok := maintDone[s.ID]; ok {
				done(i, secs)
				if maintFail[s.ID] {
					s.State = "fail"
				}
			}
		}
	}

	// 后面已经有完成（或已开始的 UAC 内）步骤时，前面还在等的就是脚本这次跳过了（例如 Desktop 版本相同不重装）。
	// UAC 内的步骤只有 进度.log 写了「开始」才算在跑。
	last := -1
	for i := range steps {
		if steps[i].State == "done" || steps[i].State == "fail" || steps[i].UAC && maintStarted[steps[i].ID] {
			last = i
		}
	}
	for i := 0; i < last; i++ {
		if steps[i].State == "wait" {
			steps[i].State = "skip"
		}
	}
	// 第一个还在等（或失败）的步骤就是正在跑（或停住）的。
	cur := -1
	for i := range steps {
		if steps[i].State == "wait" || steps[i].State == "fail" {
			cur = i
			break
		}
	}
	if cur >= 0 && steps[cur].State == "wait" && alive {
		s := &steps[cur]
		s.State = "run"
		s.Secs = round1(obs.since(s.ID, now))
		if s.ID == "uac" && (rec == nil || rec.Command == "") {
			// 还在准备 UAC 命令，没真正弹出；秒表从命令写下那一刻算
			delete(obs.seen, "uac")
			s.Secs = 0
		}
		if s.ID == "download" {
			dl = downloadProgress(root, s.Secs)
		}
	}
	return snapshot{Steps: steps, Cur: cur, Download: dl}
}

// downloadProgress 读下载中的临时文件大小；总大小按上一个已下载的安装包估，没有就按 300 MB。
func downloadProgress(root string, secs float64) *Download {
	dir := filepath.Join(root, "Desktop安装包下载")
	ents, _ := os.ReadDir(dir)
	d := &Download{TotalMB: 300}
	var newest time.Time
	for _, e := range ents {
		info, err := e.Info()
		if err != nil || e.IsDir() || !strings.HasSuffix(e.Name(), ".msix") {
			continue
		}
		mb := float64(info.Size()) / (1 << 20)
		if strings.HasSuffix(e.Name(), "-下载中.msix") {
			d.MB = mb
		} else if info.ModTime().After(newest) {
			newest, d.TotalMB = info.ModTime(), mb
		}
	}
	if d.MB > d.TotalMB {
		d.TotalMB = d.MB
	}
	if secs > 0 {
		d.Speed = round1(d.MB / secs)
	}
	d.MB, d.TotalMB = round1(d.MB), round1(d.TotalMB)
	return d
}

// remaining 估剩余秒数：正在跑的那步按预计减已用（超时了按 2 秒算），后面的步骤按预计；
// 下载跑了 5 秒以上时按实际速度估。
func remaining(steps []Step, cur int, dl *Download) float64 {
	if cur < 0 {
		return 0
	}
	r := 0.0
	for i := cur; i < len(steps); i++ {
		s := steps[i]
		switch s.State {
		case "run":
			left := s.Est - s.Secs
			if s.ID == "download" && dl != nil && s.Secs > 5 && dl.Speed > 0.05 {
				left = (dl.TotalMB - dl.MB) / dl.Speed
			}
			if left < 2 {
				left = 2
			}
			r += left
		case "wait":
			r += s.Est
		}
	}
	return round1(r)
}

func round1(v float64) float64 {
	if v < 0 {
		return 0
	}
	return float64(int(v*10+0.5)) / 10
}
