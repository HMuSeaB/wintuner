// Package webui 提供内嵌的图形界面。
//
// 界面是一份 HTML，用 //go:embed 编进二进制，启动时只在 127.0.0.1 上开一个
// 本地服务，再用系统浏览器打开。仍然是单文件分发、零运行时依赖。
package webui

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/HMuSeaB/wintuner/internal/autostart"
	"github.com/HMuSeaB/wintuner/internal/diag"
	"github.com/HMuSeaB/wintuner/internal/elevate"
	"github.com/HMuSeaB/wintuner/internal/inject"
	"github.com/HMuSeaB/wintuner/internal/overlays"
	"github.com/HMuSeaB/wintuner/internal/shell"
)

//go:embed assets/index.html
var assets embed.FS

// Options 是构造服务端的输入。
type Options struct {
	Version string
	Addr    string // 留空则用 127.0.0.1:0
	Token   string // 留空则随机生成

	// IdleTimeout 是无人访问后自动关闭的等待时长，0 表示不自动关闭。
	//
	// 存在的理由：界面跑在浏览器里，用户直接关掉标签页时服务端无从知晓。
	// 双击启动时进程没有控制台窗口，少了这个兜底，残留进程只能去任务管理器收拾。
	IdleTimeout time.Duration
}

// Server 是内嵌界面的本地服务端。
type Server struct {
	opts  Options
	token string

	ln   net.Listener
	http *http.Server

	mu         sync.Mutex
	lastActive time.Time

	done     chan struct{}
	doneOnce sync.Once
}

// New 构造服务端，尚未开始监听。
func New(opts Options) (*Server, error) {
	token := opts.Token
	if token == "" {
		var buf [16]byte
		if _, err := rand.Read(buf[:]); err != nil {
			return nil, fmt.Errorf("生成访问令牌失败: %w", err)
		}
		token = hex.EncodeToString(buf[:])
	}
	return &Server{
		opts:       opts,
		token:      token,
		lastActive: time.Now(),
		done:       make(chan struct{}),
	}, nil
}

// Start 开始监听。
func (s *Server) Start() error {
	addr := s.opts.Addr
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("无法监听 %s: %w", addr, err)
	}
	s.ln = ln

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/state", s.guard(s.handleState))
	mux.HandleFunc("/api/toggle", s.guard(s.handleToggle))
	mux.HandleFunc("/api/diag", s.guard(s.handleDiag))
	mux.HandleFunc("/api/now", s.guard(s.handleNow))
	mux.HandleFunc("/api/repeated", s.guard(s.handleRepeated))
	mux.HandleFunc("/api/reveal", s.guard(s.handleReveal))
	mux.HandleFunc("/api/restart-explorer", s.guard(s.handleRestartExplorer))
	mux.HandleFunc("/api/kill", s.guard(s.handleKill))
	mux.HandleFunc("/api/elevate", s.guard(s.handleElevate))
	mux.HandleFunc("/api/quit", s.guard(s.handleQuit))
	mux.HandleFunc("/api/ping", s.guard(s.handlePing))

	s.http = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      60 * time.Second, // 采样接口可能跑几秒
	}
	go func() { _ = s.http.Serve(ln) }()
	go s.watchIdle()
	return nil
}

// URL 返回带令牌的访问地址。
func (s *Server) URL() string {
	return fmt.Sprintf("http://%s/?t=%s", s.ln.Addr().String(), s.token)
}

// Addr 返回实际监听地址。
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Done 在界面请求关闭或空闲超时后关闭。
func (s *Server) Done() <-chan struct{} { return s.done }

// Shutdown 停止服务。
func (s *Server) Shutdown() {
	s.doneOnce.Do(func() {
		close(s.done)
		if s.http != nil {
			_ = s.http.Close()
		}
	})
}

// touch 记录一次活动，供空闲判定使用。
func (s *Server) touch() {
	s.mu.Lock()
	s.lastActive = time.Now()
	s.mu.Unlock()
}

// watchIdle 在长时间无人访问后关闭服务。
func (s *Server) watchIdle() {
	d := s.opts.IdleTimeout
	if d <= 0 {
		return
	}
	// 检查间隔取超时的四分之一，误差不超过 25%；
	// 下限只为让测试跑得快，真实取值（分钟级）不受影响。
	tick := d / 4
	if tick < 100*time.Millisecond {
		tick = 100 * time.Millisecond
	}
	t := time.NewTicker(tick)
	defer t.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			s.mu.Lock()
			idle := time.Since(s.lastActive)
			s.mu.Unlock()
			if idle >= d {
				s.Shutdown()
				return
			}
		}
	}
}

// guard 校验令牌。
//
// 防本地 CSRF：任何网页都能向 127.0.0.1 发请求，但没有令牌就调不动接口。
func (s *Server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.touch()
		got := r.Header.Get("X-Wintuner-Token")
		if got == "" {
			got = r.URL.Query().Get("t")
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			writeErr(w, http.StatusForbidden, "令牌无效")
			return
		}
		next(w, r)
	}
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.touch()
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	// 首页也要校验令牌：否则任何网页都能把它嵌进 iframe 里诱导点击。
	got := r.URL.Query().Get("t")
	if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("令牌无效。请通过 wintuner gui 重新打开界面。\n"))
		return
	}

	page, err := assets.ReadFile("assets/index.html")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "界面资源缺失")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(page)
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]bool{"ok": true})
}

// ---------- 数据形状 ----------

// State 是界面首屏所需的全部数据。
type State struct {
	Version    string            `json:"version"`
	Admin      bool              `json:"admin"`
	ParkingDir string            `json:"parkingDir"`
	Overlays   []overlays.Item   `json:"overlays"`
	Injections []inject.Provider `json:"injections"`
	Items      []autostart.Item  `json:"items"`
	// Warnings 收集各来源读失败的原因，界面上要如实显示，
	// 不能让用户以为"没列出就是没有"。
	Warnings []string `json:"warnings"`
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	st := State{
		Version:    s.opts.Version,
		Admin:      elevate.IsAdmin(),
		ParkingDir: autostart.ParkingDir(),
	}

	if v, err := overlays.List(); err == nil {
		st.Overlays = v
	} else {
		st.Warnings = append(st.Warnings, "图标叠加读取失败: "+err.Error())
	}

	if v, err := inject.List(); err == nil {
		st.Injections = v
	} else {
		st.Warnings = append(st.Warnings, "Winsock 提供程序读取失败: "+err.Error())
	}

	// 启动项扫描较慢（要跑 schtasks、枚举上千个服务子键），
	// 单独失败不拖累其它两块。
	if v, err := autostart.Scan(); err == nil {
		st.Items = v
	} else {
		st.Warnings = append(st.Warnings, "启动项扫描失败: "+err.Error())
	}

	writeJSON(w, st)
}

// toggleRequest 是启用 / 禁用的请求。
type toggleRequest struct {
	// Kind 决定作用于哪一类：autostart / overlay / injection
	Kind string `json:"kind"`
	ID   string `json:"id"`
	// Enable 为 true 是启用，false 是禁用。
	Enable bool `json:"enable"`
}

func (s *Server) handleToggle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只接受 POST")
		return
	}
	var req toggleRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无法解析")
		return
	}
	if req.ID == "" {
		writeErr(w, http.StatusBadRequest, "缺少标识")
		return
	}

	var err error
	switch req.Kind {
	case "autostart":
		if req.Enable {
			err = autostart.Enable(req.ID)
		} else {
			err = autostart.Disable(req.ID)
		}
	case "overlay":
		if req.Enable {
			err = overlays.Enable(req.ID)
		} else {
			err = overlays.Disable(req.ID)
		}
	case "injection":
		if req.Enable {
			err = inject.Enable(req.ID)
		} else {
			err = inject.Disable(req.ID)
		}
	default:
		writeErr(w, http.StatusBadRequest, "未知类别: "+req.Kind)
		return
	}
	if err != nil {
		// 权限不足是最常见的一类失败，给出可操作的提示而不是干巴巴的错误码。
		msg := err.Error()
		if !elevate.IsAdmin() && strings.Contains(strings.ToLower(msg), "access is denied") {
			msg = "需要管理员权限：请以管理员身份重新运行 wintuner"
		}
		writeErr(w, http.StatusBadRequest, msg)
		return
	}

	// 回一份最新状态，界面不用再单独刷新一次。
	s.handleState(w, r)
}

// diagRequest 控制诊断范围。
type diagRequest struct {
	SampleMS int `json:"sampleMs"`
}

func (s *Server) handleDiag(w http.ResponseWriter, r *http.Request) {
	ms := 2000
	if r.Method == http.MethodPost {
		var req diagRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err == nil && req.SampleMS > 0 {
			ms = req.SampleMS
		}
	}
	// 采样别太长，否则浏览器会觉得请求挂了。
	if ms > 10000 {
		ms = 10000
	}
	if ms < 300 {
		ms = 300
	}

	explorer, err := diag.SampleExplorer(ms)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	groups, err := diag.RepeatedProcesses(6)
	if err != nil {
		// 重复进程统计失败不致命，主数据仍要返回。
		groups = nil
	}

	writeJSON(w, map[string]any{
		"explorer":  explorer,
		"repeated":  groups,
		"admin":     elevate.IsAdmin(),
		"sampledMs": ms,
	})
}

// killRequest 请求结束一组同名进程。
type killRequest struct {
	Name string `json:"name"`
}

// handleNow 返回 Explorer 的即时负载，供界面轮询。
//
// 单独一个短采样接口而不是复用 /api/diag：诊断要跑 schtasks、枚举上千个
// 服务子键，几百毫秒起步；而"现在卡不卡"要的是轻量、可频繁调用的读数。
func (s *Server) handleNow(w http.ResponseWriter, r *http.Request) {
	now, err := diag.SampleExplorerLight()
	if err != nil {
		// 这里不返回错误码：explorer 短暂不在（正在重启、或恰好被结束）
		// 是正常现象，界面按"未运行"显示即可，不该让轮询报错。
		writeJSON(w, map[string]any{
			"running": false,
			"verdict": "资源管理器未在运行（可能正在重启）",
		})
		return
	}
	writeJSON(w, now)
}

// handleRepeated 单独给出"同名进程过多的"列表。
//
// 与 /api/now 一起做轮询：这两个数据变化快（结束进程后立刻该更新），
// 而启动项、角标那些不适合频繁全量重扫。
func (s *Server) handleRepeated(w http.ResponseWriter, r *http.Request) {
	groups, err := diag.RepeatedProcesses(6)
	if err != nil {
		writeJSON(w, []any{})
		return
	}
	writeJSON(w, groups)
}

// handleRestartExplorer 重启资源管理器。
//
// 这个操作有真实风险：早先的实现只做"杀掉再起"，在真机上把用户桌面搞没了——
// 进程起来了、5 秒后自己走了，而接口返回了成功。现在 diag.RestartExplorer
// 内部会逐步验证并且有兜底路径，这里只负责把过程如实转达给界面。
func (s *Server) handleRestartExplorer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只接受 POST")
		return
	}
	res, err := diag.RestartExplorer()
	if err != nil {
		// 把每一步的成败一并带上：用户需要知道工具试过什么、为什么没成。
		writeJSONCode(w, http.StatusInternalServerError, map[string]any{
			"error":    err.Error(),
			"attempts": res.Attempts,
		})
		return
	}
	now, _ := diag.SampleExplorerLight()
	writeJSON(w, map[string]any{
		"ok":       true,
		"method":   res.Method,
		"oldPid":   res.OldPID,
		"newPid":   res.NewPID,
		"attempts": res.Attempts,
		"now":      now,
		// 说明重启后的头十几秒负载偏高是正常的，免得用户以为没修好。
		"note": "刚重启的十几秒内负载偏高属正常（重建桌面与加载图标）；20 秒后的读数才是基线。",
	})
}

// revealRequest 请求在资源管理器里定位一个路径。
type revealRequest struct {
	// Path 是要定位的文件或目录。
	Path string `json:"path"`
	// Kind 决定行为：file 打开所在目录并选中该项，dir 直接打开目录。
	Kind string `json:"kind"`
}

// handleReveal 在资源管理器里打开某个文件或目录的位置。
//
// 存在的意义：启动项列表里给的是命令字符串，用户看到 "Nexu.exe --foo"
// 没法知道它在哪、要不要留。能一键跳过去看一眼，比读路径快得多。
func (s *Server) handleReveal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只接受 POST")
		return
	}
	var req revealRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无法解析")
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		writeErr(w, http.StatusBadRequest, "缺少路径")
		return
	}

	var err error
	if req.Kind == "dir" {
		err = shell.OpenDir(req.Path)
	} else {
		err = shell.RevealFile(req.Path)
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleKill 结束一组同名进程。
//
// 这是"卡死的进程拖累 shell"这类问题的直接解法：实测中一次 Explorer 卡顿
// 就是 23 个没有窗口的 SnippingTool 造成的，杀掉即可，无需重启。
func (s *Server) handleKill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只接受 POST")
		return
	}
	var req killRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体无法解析")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "缺少进程名")
		return
	}
	// 挡住误伤：系统关键进程不允许通过界面结束。
	if diag.IsProtected(req.Name) {
		writeErr(w, http.StatusBadRequest, "该系统进程不允许结束")
		return
	}

	n, err := diag.KillByName(req.Name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]any{"killed": n, "name": req.Name})
}

func (s *Server) handleElevate(w http.ResponseWriter, r *http.Request) {
	if elevate.IsAdmin() {
		writeJSON(w, map[string]any{"already": true})
		return
	}
	writeJSON(w, map[string]any{"already": false})
	// 先回响应再自我重启，否则界面拿不到确认。
	go func() {
		time.Sleep(150 * time.Millisecond)
		if err := elevate.Relaunch("gui"); err == nil {
			s.Shutdown()
		}
	}()
}

func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]bool{"ok": true})
	go func() {
		time.Sleep(120 * time.Millisecond)
		s.Shutdown()
	}()
}

// ---------- 输出辅助 ----------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	// 路径里会有中文，不要转成 \uXXXX。
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(map[string]string{"error": msg})
}

// writeJSONCode 与 writeJSON 相同，但指定状态码。
func writeJSONCode(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// NormalizeAddr 把用户给的 --addr 收拢到回环地址。
//
// 这个服务能改动系统配置，暴露到局域网等于把机器交出去，
// 因此直接拒绝而不是警告放行。
func NormalizeAddr(addr string) (string, error) {
	if addr == "" {
		return "", nil
	}
	host := addr
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host = addr[:i]
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return addr, nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", fmt.Errorf("只允许监听回环地址，收到 %q", addr)
	}
	return addr, nil
}
