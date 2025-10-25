package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"image/color"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
	"gopkg.in/yaml.v3"
)

//go:embed GVAPanel.png
var iconData []byte

// GVAConfig GVA的config.yaml结构
type GVAConfig struct {
	System struct {
		Addr     int  `yaml:"addr"`
		UseRedis bool `yaml:"use-redis"`
	} `yaml:"system"`
	Redis struct {
		Addr     string `yaml:"addr"`
		Password string `yaml:"password"`
		DB       int    `yaml:"db"`
	} `yaml:"redis"`
}

// Config 配置结构（简化版）
type Config struct {
	GVARootPath string `json:"gva_root_path"` // GVA 安装目录
}

// ServiceInfo 服务信息
type ServiceInfo struct {
	IsRunning bool
	Port      int
	StartTime time.Time
	Process   *os.Process
}

// GVALauncher 启动器主结构
type GVALauncher struct {
	config          Config
	backendService  ServiceInfo
	frontendService ServiceInfo
	backendPort     int  // 从 GVA config.yaml 读取的后端端口
	frontendPort    int  // 前端端口（默认 8080）
	
	// 屏幕信息
	screenWidth  float32
	screenHeight float32
	
	// 窗口尺寸（基于屏幕分辨率计算）
	windowWidth  float32
	windowHeight float32
	
	// UI 组件
	window              fyne.Window
	gvaPathEntry        *widget.Entry
	depStatusLabel      *widget.Label
	frontendDepLabel    *widget.Label  // 前端依赖状态
	backendDepLabel     *widget.Label  // 后端依赖状态
	backendStatusLabel  *widget.Label
	frontendStatusLabel *widget.Label
	urlLabel            *widget.Label
	startButton         *widget.Button
	stopButton          *widget.Button
	checkDepsButton     *widget.Button
	installDepsButton   *widget.Button
	frontendMirrorEntry *widget.Entry
	backendMirrorEntry  *widget.Entry
	
	// Redis 配置组件
	redisSwitch      *widget.Check
	redisAddrEntry   *widget.Entry
	redisPassEntry   *widget.Entry
	redisDBEntry     *widget.Entry
	redisTestBtn     *widget.Button
	redisSaveBtn     *widget.Button
	redisCancelBtn   *widget.Button
	
	// Redis 配置缓存（用于取消操作）
	cachedRedisConfig struct {
		UseRedis bool
		Addr     string
		Password string
		DB       int
	}
	
	// 状态监控控制
	pauseStatusMonitor bool
	
	// 响应式按钮列表（用于窗口大小改变时刷新）
	responsiveButtons []*ResponsiveButton
}

// ========================================
// 响应式按钮容器
// ========================================

// ResponsiveButton 响应式按钮容器，按钮宽度会根据窗口大小动态调整
type ResponsiveButton struct {
	widget.BaseWidget
	button    *widget.Button
	widthVW   float32
	launcher  *GVALauncher
	container *fyne.Container
}

// NewResponsiveButton 创建响应式按钮
func NewResponsiveButton(launcher *GVALauncher, button *widget.Button, widthVW float32) *ResponsiveButton {
	rb := &ResponsiveButton{
		button:   button,
		widthVW:  widthVW,
		launcher: launcher,
	}
	rb.ExtendBaseWidget(rb)
	rb.updateSize()
	return rb
}

// updateSize 更新按钮尺寸
func (rb *ResponsiveButton) updateSize() {
	width := rb.launcher.calcVW(rb.widthVW)
	rb.container = container.NewMax(
		canvas.NewRectangle(color.Transparent),
		rb.button,
	)
	// 强制设置最小尺寸
	rb.container.Resize(fyne.NewSize(width, 0))
}

// CreateRenderer 创建渲染器
func (rb *ResponsiveButton) CreateRenderer() fyne.WidgetRenderer {
	rb.updateSize()
	return widget.NewSimpleRenderer(rb.container)
}

// Refresh 刷新组件（窗口大小改变时调用）
func (rb *ResponsiveButton) Refresh() {
	rb.updateSize()
	rb.BaseWidget.Refresh()
}

func main() {
	launcher := &GVALauncher{}
	launcher.loadConfig()  // 加载配置（如果不存在会自动检测屏幕尺寸并创建）
	launcher.createUI()
}

// ========================================
// 辅助函数
// ========================================

// createHiddenCmd 创建一个隐藏控制台窗口的命令（Windows专用）
func createHiddenCmd(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	}
	return cmd
}

// ========================================
// 屏幕分辨率检测
// ========================================

// detectScreenSize 跨平台检测屏幕分辨率（逻辑分辨率）
func (l *GVALauncher) detectScreenSize() {
	// 默认值（适用于大多数屏幕）
	l.screenWidth = 1920
	l.screenHeight = 1080
	
	switch runtime.GOOS {
	case "windows":
		l.detectScreenSizeWindows()
	case "darwin":  // macOS
		l.detectScreenSizeMacOS()
	case "linux":
		l.detectScreenSizeLinux()
	default:
		// 其他系统使用默认值
		// 未知操作系统，使用默认分辨率
	}
}

// detectScreenSizeWindows Windows 平台屏幕检测
func (l *GVALauncher) detectScreenSizeWindows() {
	cmd := createHiddenCmd("powershell", "-Command",
		"Add-Type -AssemblyName System.Windows.Forms; "+
			"$screen = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds; "+
			"Write-Output \"$($screen.Width)x$($screen.Height)\"")
	output, err := cmd.Output()
	if err == nil {
		resolution := strings.TrimSpace(string(output))
		parts := strings.Split(resolution, "x")
		if len(parts) == 2 {
			if width, err := strconv.Atoi(parts[0]); err == nil && width > 0 {
				l.screenWidth = float32(width)
			}
			if height, err := strconv.Atoi(parts[1]); err == nil && height > 0 {
				l.screenHeight = float32(height)
			}
		}
	}
}

// detectScreenSizeMacOS macOS 平台屏幕检测
func (l *GVALauncher) detectScreenSizeMacOS() {
	// 方法1：使用 system_profiler（推荐）
	cmd := exec.Command("system_profiler", "SPDisplaysDataType")
	output, err := cmd.Output()
	if err == nil {
		outputStr := string(output)
		// 查找 "Resolution:" 行
		// 格式示例：Resolution: 2560 x 1440
		lines := strings.Split(outputStr, "\n")
		for _, line := range lines {
			if strings.Contains(line, "Resolution:") {
				// 提取分辨率
				parts := strings.Fields(line)
				for i, part := range parts {
					if part == "Resolution:" && i+3 < len(parts) {
						if width, err := strconv.Atoi(parts[i+1]); err == nil && width > 0 {
							l.screenWidth = float32(width)
						}
						if height, err := strconv.Atoi(parts[i+3]); err == nil && height > 0 {
							l.screenHeight = float32(height)
						}
						return
					}
				}
			}
		}
	}
	
	// 方法2：使用 osascript 作为备用
	cmd = exec.Command("osascript", "-e",
		"tell application \"Finder\" to get bounds of window of desktop")
	output, err = cmd.Output()
	if err == nil {
		// 输出格式：0, 0, 2560, 1440
		outputStr := strings.TrimSpace(string(output))
		parts := strings.Split(outputStr, ", ")
		if len(parts) == 4 {
			if width, err := strconv.Atoi(parts[2]); err == nil && width > 0 {
				l.screenWidth = float32(width)
			}
			if height, err := strconv.Atoi(parts[3]); err == nil && height > 0 {
				l.screenHeight = float32(height)
			}
		}
	}
}

// detectScreenSizeLinux Linux 平台屏幕检测
func (l *GVALauncher) detectScreenSizeLinux() {
	// 方法1：使用 xrandr（最常见）
	cmd := exec.Command("xrandr")
	output, err := cmd.Output()
	if err == nil {
		outputStr := string(output)
		lines := strings.Split(outputStr, "\n")
		for _, line := range lines {
			// 查找当前活动分辨率（带 * 号的行）
			// 格式示例：   1920x1080     60.00*+
			if strings.Contains(line, "*") {
				fields := strings.Fields(line)
				if len(fields) > 0 {
					resolution := fields[0]
					parts := strings.Split(resolution, "x")
					if len(parts) == 2 {
						if width, err := strconv.Atoi(parts[0]); err == nil && width > 0 {
							l.screenWidth = float32(width)
						}
						if height, err := strconv.Atoi(parts[1]); err == nil && height > 0 {
							l.screenHeight = float32(height)
						}
						return
					}
				}
			}
		}
	}
	
	// 方法2：使用 xdpyinfo 作为备用
	cmd = exec.Command("xdpyinfo")
	output, err = cmd.Output()
	if err == nil {
		outputStr := string(output)
		lines := strings.Split(outputStr, "\n")
		for _, line := range lines {
			// 查找 "dimensions:" 行
			// 格式示例：  dimensions:    1920x1080 pixels (508x285 millimeters)
			if strings.Contains(line, "dimensions:") {
				fields := strings.Fields(line)
				for i, field := range fields {
					if field == "dimensions:" && i+1 < len(fields) {
						resolution := fields[i+1]
						parts := strings.Split(resolution, "x")
						if len(parts) == 2 {
							if width, err := strconv.Atoi(parts[0]); err == nil && width > 0 {
								l.screenWidth = float32(width)
							}
							if height, err := strconv.Atoi(parts[1]); err == nil && height > 0 {
								l.screenHeight = float32(height)
							}
							return
						}
					}
				}
			}
		}
	}
	
	// 方法3：尝试读取 /sys/class/graphics/fb0/virtual_size（直接帧缓冲）
	data, err := ioutil.ReadFile("/sys/class/graphics/fb0/virtual_size")
	if err == nil {
		resolution := strings.TrimSpace(string(data))
		parts := strings.Split(resolution, ",")
		if len(parts) == 2 {
			if width, err := strconv.Atoi(strings.TrimSpace(parts[0])); err == nil && width > 0 {
				l.screenWidth = float32(width)
			}
			if height, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil && height > 0 {
				l.screenHeight = float32(height)
			}
		}
	}
}

// ========================================
// vh/vw 视口单位辅助函数（类似 CSS）
// ========================================

// vh 创建垂直间距 - 基于窗口高度的百分比
// 参数 v 相当于 CSS 中的 vh 单位
// 例如：vh(2) 相当于 CSS 的 2vh
func (l *GVALauncher) vh(v float32) *canvas.Rectangle {
	height := l.windowHeight * (v / 100)
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(1, height))
	return spacer
}

// vw 创建水平间距 - 基于窗口宽度的百分比
// 参数 v 相当于 CSS 中的 vw 单位
// 例如：vw(2) 相当于 CSS 的 2vw
func (l *GVALauncher) vw(v float32) *canvas.Rectangle {
	width := l.windowWidth * (v / 100)
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(width, 1))
	return spacer
}

// calcVH 计算 vh 值（用于需要数值的地方）
func (l *GVALauncher) calcVH(v float32) float32 {
	return l.windowHeight * (v / 100)
}

// calcVW 计算 vw 值（用于需要数值的地方）
func (l *GVALauncher) calcVW(v float32) float32 {
	return l.windowWidth * (v / 100)
}

// getExeDir 获取可执行文件所在目录
func getExeDir() string {
	exePath, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exePath)
}

// getConfigPath 获取配置文件路径
func getConfigPath() string {
	return filepath.Join(getExeDir(), ".gva-launcher.json")
}

// getGVAConfigPath 获取GVA配置文件路径
func (l *GVALauncher) getGVAConfigPath() string {
	if l.config.GVARootPath == "" {
		return ""
	}
	return filepath.Join(l.config.GVARootPath, "server", "config.yaml")
}

// readGVAConfig 读取GVA的配置文件
func (l *GVALauncher) readGVAConfig() (*GVAConfig, error) {
	configPath := l.getGVAConfigPath()
	if configPath == "" {
		return nil, fmt.Errorf("GVA根目录未设置")
	}
	
	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	
	var gvaConfig GVAConfig
	err = yaml.Unmarshal(data, &gvaConfig)
	if err != nil {
		return nil, err
	}
	
	return &gvaConfig, nil
}

// writeGVAConfig 写入GVA配置文件的端口（同时更新前端环境配置）
func (l *GVALauncher) writeGVAConfig(backendPort int) error {
	configPath := l.getGVAConfigPath()
	if configPath == "" {
		return fmt.Errorf("GVA根目录未设置")
	}
	
	// 1. 更新后端配置文件
	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("读取后端配置文件失败: %v", err)
	}
	
	var gvaConfig map[string]interface{}
	err = yaml.Unmarshal(data, &gvaConfig)
	if err != nil {
		return fmt.Errorf("解析后端配置文件失败: %v", err)
	}
	
	// 修改后端端口
	if system, ok := gvaConfig["system"].(map[string]interface{}); ok {
		system["addr"] = backendPort
	}
	
	// 写回后端配置文件
	newData, err := yaml.Marshal(gvaConfig)
	if err != nil {
		return fmt.Errorf("序列化后端配置失败: %v", err)
	}
	
	err = ioutil.WriteFile(configPath, newData, 0644)
	if err != nil {
		return fmt.Errorf("写入后端配置文件失败: %v", err)
	}
	
	// 2. 更新前端环境配置文件
	err = l.writeFrontendBackendPort(backendPort)
	if err != nil {
		return fmt.Errorf("更新前端环境配置失败: %v", err)
	}
	
	return nil
}

// writeFrontendConfig 写入前端配置文件的端口（同时更新环境配置）
func (l *GVALauncher) writeFrontendConfig(frontendPort int) error {
	if l.config.GVARootPath == "" {
		return fmt.Errorf("GVA根目录未设置")
	}
	
	webPath := filepath.Join(l.config.GVARootPath, "web")
	
	// 1. 更新 .env 文件（如果存在）
	envPath := filepath.Join(webPath, ".env")
	if l.fileExists(envPath) {
		// 读取现有 .env 文件
		data, err := ioutil.ReadFile(envPath)
		if err != nil {
			return fmt.Errorf("读取 .env 文件失败: %v", err)
		}
		
		lines := strings.Split(string(data), "\n")
		updated := false
		
		// 更新现有的 PORT 或 VUE_APP_PORT 行
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "PORT=") {
				lines[i] = fmt.Sprintf("PORT=%d", frontendPort)
				updated = true
				break
			} else if strings.HasPrefix(strings.TrimSpace(line), "VUE_APP_PORT=") {
				lines[i] = fmt.Sprintf("VUE_APP_PORT=%d", frontendPort)
				updated = true
				break
			}
		}
		
		// 如果没有找到现有的端口配置，添加新的
		if !updated {
			lines = append(lines, fmt.Sprintf("PORT=%d", frontendPort))
		}
		
		// 写回文件
		newContent := strings.Join(lines, "\n")
		err = ioutil.WriteFile(envPath, []byte(newContent), 0644)
		if err != nil {
			return fmt.Errorf("写入 .env 文件失败: %v", err)
		}
	}
	
	// 2. 更新或创建 .env.development 文件
	err := l.writeFrontendPortToEnvDev(frontendPort)
	if err != nil {
		return fmt.Errorf("更新 .env.development 文件失败: %v", err)
	}
	
	return nil
}

// writeFrontendBackendPort 写入前端环境配置文件的后端端口
func (l *GVALauncher) writeFrontendBackendPort(backendPort int) error {
	if l.config.GVARootPath == "" {
		return fmt.Errorf("GVA根目录未设置")
	}
	
	webPath := filepath.Join(l.config.GVARootPath, "web")
	
	// 1. 优先尝试写入 .env.development 文件
	envPath := filepath.Join(webPath, ".env.development")
	if l.fileExists(envPath) {
		// 读取现有 .env.development 文件
		data, err := ioutil.ReadFile(envPath)
		if err != nil {
			return fmt.Errorf("读取 .env.development 文件失败: %v", err)
		}
		
		lines := strings.Split(string(data), "\n")
		updated := false
		
		// 更新现有的 VITE_SERVER_PORT 行
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "VITE_SERVER_PORT=") {
				lines[i] = fmt.Sprintf("VITE_SERVER_PORT=%d", backendPort)
				updated = true
				break
			}
		}
		
		// 如果没有找到现有的后端端口配置，添加新的
		if !updated {
			lines = append(lines, fmt.Sprintf("VITE_SERVER_PORT=%d", backendPort))
		}
		
		// 写回文件
		newContent := strings.Join(lines, "\n")
		return ioutil.WriteFile(envPath, []byte(newContent), 0644)
	}
	
	// 2. 如果 .env.development 不存在，创建新的文件
	envContent := fmt.Sprintf(`# 前端开发环境配置
VITE_CLI_PORT=8080
VITE_SERVER_PORT=%d
VITE_BASE_PATH=http://127.0.0.1
VITE_BASE_API=/api
`, backendPort)
	return ioutil.WriteFile(envPath, []byte(envContent), 0644)
}

// writeFrontendPortToEnvDev 写入前端环境配置文件的前端端口
func (l *GVALauncher) writeFrontendPortToEnvDev(frontendPort int) error {
	if l.config.GVARootPath == "" {
		return fmt.Errorf("GVA根目录未设置")
	}
	
	webPath := filepath.Join(l.config.GVARootPath, "web")
	envPath := filepath.Join(webPath, ".env.development")
	
	if l.fileExists(envPath) {
		// 读取现有 .env.development 文件
		data, err := ioutil.ReadFile(envPath)
		if err != nil {
			return fmt.Errorf("读取 .env.development 文件失败: %v", err)
		}
		
		lines := strings.Split(string(data), "\n")
		updated := false
		
		// 更新现有的 VITE_CLI_PORT 行
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "VITE_CLI_PORT=") {
				lines[i] = fmt.Sprintf("VITE_CLI_PORT=%d", frontendPort)
				updated = true
				break
			}
		}
		
		// 如果没有找到现有的前端端口配置，添加新的
		if !updated {
			lines = append(lines, fmt.Sprintf("VITE_CLI_PORT=%d", frontendPort))
		}
		
		// 写回文件
		newContent := strings.Join(lines, "\n")
		return ioutil.WriteFile(envPath, []byte(newContent), 0644)
	}
	
	// 如果 .env.development 不存在，创建新的文件
	envContent := fmt.Sprintf(`# 前端开发环境配置
VITE_CLI_PORT=%d
VITE_SERVER_PORT=8888
VITE_BASE_PATH=http://127.0.0.1
VITE_BASE_API=/api
`, frontendPort)
	return ioutil.WriteFile(envPath, []byte(envContent), 0644)
}

// readFrontendMirror 读取前端镜像源（从 .npmrc 或 npm config）
func (l *GVALauncher) readFrontendMirror() string {
	if l.config.GVARootPath == "" {
		return ""
	}
	
	webPath := filepath.Join(l.config.GVARootPath, "web")
	if !l.dirExists(webPath) {
		return ""
	}
	
	cmd := createHiddenCmd("npm", "config", "get", "registry")
	cmd.Dir = webPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	
	registry := strings.TrimSpace(string(output))
	return registry
}

// readBackendMirror 读取后端镜像源（从 GOPROXY 环境变量）
func (l *GVALauncher) readBackendMirror() string {
	if l.config.GVARootPath == "" {
		return ""
	}
	
	// 检查server目录是否存在
	serverPath := filepath.Join(l.config.GVARootPath, "server")
	if !l.dirExists(serverPath) {
		return ""
	}
	
	cmd := createHiddenCmd("go", "env", "GOPROXY")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	
	proxy := strings.TrimSpace(string(output))
	return proxy
}

// updateFrontendMirror 更新前端镜像源
func (l *GVALauncher) updateFrontendMirror(mirrorURL string) error {
	if l.config.GVARootPath == "" {
		return fmt.Errorf("请先指定 GVA 根目录")
	}
	
	webPath := filepath.Join(l.config.GVARootPath, "web")
	
	// 如果为空，恢复默认官方源
	if mirrorURL == "" {
		mirrorURL = "https://registry.npmjs.org/"
	}
	
	cmd := createHiddenCmd("npm", "config", "set", "registry", mirrorURL)
	cmd.Dir = webPath
	
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("设置 npm 镜像源失败: %v", err)
	}
	
	return nil
}

// updateBackendMirror 更新后端镜像源
func (l *GVALauncher) updateBackendMirror(proxyURL string) error {
	// 如果为空，恢复默认官方代理
	if proxyURL == "" {
		proxyURL = "https://proxy.golang.org,direct"
	}
	
	cmd := createHiddenCmd("go", "env", "-w", "GOPROXY="+proxyURL)
	if err := cmd.Run(); err != nil{
		return fmt.Errorf("设置 GOPROXY 失败: %v", err)
	}
	
	return nil
}

// loadConfig 加载配置
func (l *GVALauncher) loadConfig() {
	// 每次启动都检测屏幕分辨率，并计算窗口尺寸
	l.detectScreenSize()
	l.windowWidth = l.screenWidth * 0.42  // 窗口宽度 = 屏幕宽度的 42%
	l.windowHeight = l.screenHeight * 0.89 // 窗口高度 = 屏幕高度的 89%
	
	configPath := getConfigPath()
	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		// 配置文件不存在，创建默认配置
		l.config = l.getDefaultConfig()
		l.saveConfig()  // 立即保存配置文件
		return
	}
	
	err = json.Unmarshal(data, &l.config)
	if err != nil {
		// JSON 解析失败，重新创建默认配置
		l.config = l.getDefaultConfig()
		l.saveConfig()  // 立即保存配置文件
		return
	}
}

// saveConfig 保存配置
func (l *GVALauncher) saveConfig() error {
	configPath := getConfigPath()
	data, err := json.MarshalIndent(l.config, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(configPath, data, 0644)
}

// getDefaultConfig 获取默认配置（仅在第一次启动或配置文件不存在时调用）
func (l *GVALauncher) getDefaultConfig() Config {
	return Config{
		GVARootPath: "", // GVA 安装目录（用户选择后保存）
	}
}

// createUI 创建用户界面
func (l *GVALauncher) createUI() {
	myApp := app.New()
	
	// 设置应用图标（全局）
	if len(iconData) > 0 {
		myApp.SetIcon(fyne.NewStaticResource("icon.png", iconData))
	}
	
	l.window = myApp.NewWindow("GVAPanel")
	
	// 依赖管理区域
	depArea := l.createDependencyArea()
	
	// 服务控制区域
	serviceArea := l.createServiceArea()
	
	// GVA 根目录配置区域
	pathArea := l.createPathArea()
	
	// 镜像源配置区域
	mirrorArea := l.createMirrorArea()
	
	// Redis 对接区域
	redisArea := l.createRedisArea()
	
	// 主布局（各区域已自带边界线，无需额外 Separator）
	content := container.NewVBox(
		depArea,
		serviceArea,
		pathArea,
		mirrorArea,
		redisArea,
	)
	
	l.window.SetContent(content)
	l.window.Resize(fyne.NewSize(l.windowWidth, l.windowHeight))
	l.window.CenterOnScreen()  // ⭐ 窗口居中显示
	
	// 启动时立即更新端口和地址显示
	l.updatePortsFromGVAConfig()
	
	// 启动时立即加载镜像源配置
	l.loadMirrorConfig()
	
	// 启动时立即加载 Redis 配置
	l.loadRedisConfig()
	
	// 启动时自动检测（如果已设置 GVA 根目录）
	if l.config.GVARootPath != "" {
		l.checkDependencies()
		l.checkServiceStatus()
	}
	
	// 监听窗口大小变化，刷新所有响应式按钮
	l.window.SetOnClosed(func() {
		// 窗口关闭时的清理工作
	})
	
	// 使用一个 goroutine 定期检查窗口大小
	go func() {
		lastWidth := l.window.Canvas().Size().Width
		lastHeight := l.window.Canvas().Size().Height
		
		for {
			time.Sleep(100 * time.Millisecond)
			currentSize := l.window.Canvas().Size()
			
			if currentSize.Width != lastWidth || currentSize.Height != lastHeight {
				lastWidth = currentSize.Width
				lastHeight = currentSize.Height
				
				// 注意：这里不应该修改 screenWidth/screenHeight
				// 它们应该始终保持为实际屏幕分辨率，用于计算比例
				
				// 刷新所有响应式按钮
				for _, rb := range l.responsiveButtons {
					rb.Refresh()
				}
			}
		}
	}()
	
	l.window.ShowAndRun()
}

// createDependencyArea 创建依赖管理区域
func (l *GVALauncher) createDependencyArea() *fyne.Container {
	// 1. 标题装箱 + 底部边界线
	titleBox := container.NewVBox(
		container.NewHBox(
			widget.NewLabelWithStyle("🔧 依赖管理", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		),
		widget.NewSeparator(), // 底部边界线
	)
	
	// 2. 状态信息（直接使用Label）
	l.depStatusLabel = widget.NewLabel("⚪ 未检测")
	l.frontendDepLabel = widget.NewLabel("　　• 请先指定 GVA 根目录")
	l.backendDepLabel = widget.NewLabel("")
	
	// 4. 按钮行装箱（30vw + 4个Spacer）
	l.checkDepsButton = widget.NewButton("🔍 检查依赖状态", func() {
		l.checkDependencies()
	})
	cleanCacheButton := widget.NewButton("🗑️ 清理缓存", func() {
		l.cleanAllCache()
	})
	l.installDepsButton = widget.NewButton("📦 安装依赖", func() {
		l.installDependencies()
	})
	
	// 使用 GridWithColumns 让按钮平均分配宽度
	buttonBox := container.NewGridWithColumns(3,
		l.checkDepsButton,
		cleanCacheButton,
		l.installDepsButton,
	)
	
	// 3. 三行状态文字用GridWithRows均匀分配
	statusGrid := container.NewGridWithRows(3,
		l.depStatusLabel,
		l.frontendDepLabel,
		l.backendDepLabel,
	)
	
	// 自定义小间距（2px）
	spacer1 := canvas.NewRectangle(color.Transparent)
	spacer1.SetMinSize(fyne.NewSize(1, 2))  // 标题和状态之间2px
	
	spacer2 := canvas.NewRectangle(color.Transparent)
	spacer2.SetMinSize(fyne.NewSize(1, 2))  // 状态和按钮之间2px
	
	// 使用 VBox 组合所有元素
	return container.NewVBox(
		titleBox,
		spacer1,
		statusGrid,  // 三行状态文字（均匀分配）
		spacer2,
		buttonBox,
	)
}

// createServiceArea 创建服务控制区域
func (l *GVALauncher) createServiceArea() *fyne.Container {
	// 5. 标题装箱 + 上下边界线
	titleBox := container.NewVBox(
		widget.NewSeparator(), // 上边界线
		container.NewHBox(
			widget.NewLabelWithStyle("🚀 服务控制", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		),
		widget.NewSeparator(), // 下边界线
	)
	
	// 6. 启动关闭按钮装箱（45vw + 3个Spacer）
	l.startButton = widget.NewButton("🚀 启动 GVA", func() {
		l.startGVA()
	})
	l.stopButton = widget.NewButton("🔴 关闭 GVA", func() {
		l.stopGVA()
	})
	l.stopButton.Disable()
	
	// 使用 GridWithColumns 让按钮平均分配宽度
	buttonBox := container.NewGridWithColumns(2,
		l.startButton,
		l.stopButton,
	)
	
	// 7. 状态信息装箱（5个盒子）
	// 运行状态标题
	statusTitleBox := container.NewHBox(
		widget.NewLabel("运行状态:"),
	)
	
	// 后端服务状态
	l.backendStatusLabel = widget.NewLabel("　• 后端服务: 🔴 已停止 端口: 8888")
	backendPortBtn := widget.NewButton("　⚙️ 修改　", func() {
		l.showPortDialog(true)
	})
	backendStatusBox := container.NewHBox(
		l.backendStatusLabel,
		layout.NewSpacer(),
		backendPortBtn,
	)
	
	// 前端服务状态
	l.frontendStatusLabel = widget.NewLabel("　• 前端服务: 🔴 已停止 端口: 8080")
	frontendPortBtn := widget.NewButton("　⚙️ 修改　", func() {
		l.showPortDialog(false)
	})
	frontendStatusBox := container.NewHBox(
		l.frontendStatusLabel,
		layout.NewSpacer(),
		frontendPortBtn,
	)
	
	// 访问地址标题
	urlTitleBox := container.NewHBox(
		widget.NewLabel("访问地址:"),
	)
	
	// 前端地址
	l.urlLabel = widget.NewLabel("　• 前端: 未配置")
	copyBtn := widget.NewButton("　📋 复制链接　", func() {
		if l.frontendPort > 0 {
			localIP := l.getLocalIP()
			frontendURL := fmt.Sprintf("http://%s:%d", localIP, l.frontendPort)
			l.window.Clipboard().SetContent(frontendURL)
			dialog.ShowInformation("成功", "链接已复制到剪贴板", l.window)
		} else {
			dialog.ShowInformation("提示", "端口未配置，无法复制链接", l.window)
		}
	})
	copyBtnContainer := container.NewMax(copyBtn)
	copyBtnContainer.Resize(fyne.NewSize(l.calcVW(15), 0))
	
	urlBox := container.NewHBox(
		l.urlLabel,
		layout.NewSpacer(),
		copyBtnContainer,
	)
	
	// 8. 运行状态父容器（用GridWithRows均匀分配5行）
	statusParentBox := container.NewGridWithRows(5,
		statusTitleBox,      // 第1行：运行状态标题
		backendStatusBox,    // 第2行：后端服务状态
		frontendStatusBox,   // 第3行：前端服务状态
		urlTitleBox,         // 第4行：访问地址标题
		urlBox,              // 第5行：前端地址
	)
	
	return container.NewVBox(
		titleBox,
		buttonBox,
		statusParentBox,
	)
}

// createPathArea 创建路径配置区域
func (l *GVALauncher) createPathArea() *fyne.Container {
	// 9. 标题装箱 + 上下边界线
	titleBox := container.NewVBox(
		widget.NewSeparator(), // 上边界线
		container.NewHBox(
			widget.NewLabelWithStyle("📁 GVA 根目录配置", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		),
		widget.NewSeparator(), // 下边界线
	)
	
	// 10. 浏览行装箱
	l.gvaPathEntry = widget.NewEntry()
	l.gvaPathEntry.SetPlaceHolder("请选择 GVA 根目录...")
	l.gvaPathEntry.SetText(l.config.GVARootPath)
	
	browseBtn := widget.NewButton("　📂 浏览...　", func() {
		l.showCustomFolderDialog()
	})
	
	// 用 Border 布局：右边固定按钮，中间自动填充输入框
	pathBox := container.NewBorder(
		nil, nil,      // 上下不限制
		nil,           // 左边不限制
		browseBtn,     // 右边：按钮
		l.gvaPathEntry, // 中间：输入框（自动填充）
	)
	
	return container.NewVBox(
		titleBox,
		pathBox,
	)
}

// createMirrorArea 创建镜像源配置区域
func (l *GVALauncher) createMirrorArea() *fyne.Container {
	// 11. 标题装箱 + 上下边界线
	titleBox := container.NewVBox(
		widget.NewSeparator(), // 上边界线
		container.NewHBox(
			widget.NewLabelWithStyle("🔧 镜像源配置", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		),
		widget.NewSeparator(), // 下边界线
	)
	
	// 12. 前后端镜像源装箱（2个盒子）
	// 前端镜像源
	l.frontendMirrorEntry = widget.NewEntry()
	l.frontendMirrorEntry.SetPlaceHolder("例如: https://registry.npmmirror.com")
	
	frontendUpdateBtn := widget.NewButton("　✅ 更新　", func() {
		mirrorURL := strings.TrimSpace(l.frontendMirrorEntry.Text)
		err := l.updateFrontendMirror(mirrorURL)
		if err != nil {
			dialog.ShowError(err, l.window)
		} else {
			dialog.ShowInformation("成功", "前端镜像源已更新", l.window)
		}
	})
	
	// 用 Border 布局：左边标签，右边按钮，中间输入框自动填充
	frontendBox := container.NewBorder(
		nil, nil,                          // 上下不限制
		widget.NewLabel("📦 前端镜像源:"), // 左边：标签
		frontendUpdateBtn,                 // 右边：按钮
		l.frontendMirrorEntry,            // 中间：输入框（自动填充）
	)
	
	// 后端镜像源
	l.backendMirrorEntry = widget.NewEntry()
	l.backendMirrorEntry.SetPlaceHolder("例如: https://goproxy.cn,direct")
	
	backendUpdateBtn := widget.NewButton("　✅ 更新　", func() {
		proxyURL := strings.TrimSpace(l.backendMirrorEntry.Text)
		err := l.updateBackendMirror(proxyURL)
		if err != nil {
			dialog.ShowError(err, l.window)
		} else {
			dialog.ShowInformation("成功", "后端镜像源已更新", l.window)
		}
	})
	
	// 用 Border 布局：左边标签，右边按钮，中间输入框自动填充
	backendBox := container.NewBorder(
		nil, nil,                          // 上下不限制
		widget.NewLabel("⚙️ 后端镜像源:"), // 左边：标签
		backendUpdateBtn,                  // 右边：按钮
		l.backendMirrorEntry,             // 中间：输入框（自动填充）
	)
	
	// 13. 镜像源父容器
	mirrorParentBox := container.NewVBox(
		frontendBox,
		backendBox,
	)
	
	return container.NewVBox(
		titleBox,
		mirrorParentBox,
	)
}

// createRedisArea 创建 Redis 对接配置区域
func (l *GVALauncher) createRedisArea() *fyne.Container {
	// 14. Redis 对接标题装箱 + 上下边界线
	l.redisSwitch = widget.NewCheck("启用 Redis", func(checked bool) {
		l.saveRedisSwitch(checked)
		l.updateRedisFieldsState(checked)
	})
	
	titleBox := container.NewVBox(
		widget.NewSeparator(), // 上边界线
		container.NewHBox(
			widget.NewLabelWithStyle("🔌 Redis 对接", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
			layout.NewSpacer(),
			l.redisSwitch,
		),
		widget.NewSeparator(), // 下边界线
	)
	
	// 15. Redis 配置项装箱（3个盒子，用Border让输入框填充）
	// Redis 地址
	l.redisAddrEntry = widget.NewEntry()
	l.redisAddrEntry.SetPlaceHolder("例如: 127.0.0.1:6379")
	addrBox := container.NewBorder(
		nil, nil,                       // 上下不限制
		widget.NewLabel("Redis 地址:"), // 左边：标签
		nil,                            // 右边不限制
		l.redisAddrEntry,              // 中间：输入框自动填充
	)
	
	// Redis 密码
	l.redisPassEntry = widget.NewEntry()
	l.redisPassEntry.SetPlaceHolder("没有密码可留空")
	l.redisPassEntry.Password = true
	passBox := container.NewBorder(
		nil, nil,                       // 上下不限制
		widget.NewLabel("Redis 密码:"), // 左边：标签
		nil,                            // 右边不限制
		l.redisPassEntry,              // 中间：输入框自动填充
	)
	
	// 数据库编号
	l.redisDBEntry = widget.NewEntry()
	l.redisDBEntry.SetPlaceHolder("0-15")
	dbBox := container.NewBorder(
		nil, nil,                          // 上下不限制
		widget.NewLabel("数据库编号:"),    // 左边：标签
		nil,                               // 右边不限制
		l.redisDBEntry,                   // 中间：输入框自动填充
	)
	
	// 连接测试按钮行（30vw + 4个Spacer）
	l.redisTestBtn = widget.NewButton("🔍 测试连接", func() {
		l.testRedisConnection()
	})
	l.redisSaveBtn = widget.NewButton("💾 保存", func() {
		l.saveRedisConfig()
	})
	l.redisCancelBtn = widget.NewButton("❌ 取消", func() {
		l.cancelRedisConfig()
	})
	
	// 使用 GridWithColumns 让按钮平均分配宽度
	buttonBox := container.NewGridWithColumns(3,
		l.redisTestBtn,
		l.redisSaveBtn,
		l.redisCancelBtn,
	)
	
	// 16. Redis 对接父容器
	redisParentBox := container.NewVBox(
		addrBox,
		passBox,
		dbBox,
		buttonBox,
	)
	
	return container.NewVBox(
		titleBox,
		redisParentBox,
	)
}

// ========================================
// 跨平台文件浏览辅助函数
// ========================================

// getInitialBrowsePath 获取浏览器的初始路径（跨平台）
func getInitialBrowsePath(configPath string) string {
	// 如果有配置路径且存在，使用配置路径
	if configPath != "" {
		if _, err := os.Stat(configPath); err == nil {
			return configPath
		}
	}
	
	// 根据操作系统返回默认路径
	switch runtime.GOOS {
	case "windows":
		return ""  // 空字符串代表显示驱动器列表
	default:
		// Unix-like: 返回根目录
		return "/"
	}
}

// DirItem 目录项结构（用于文件浏览）
type DirItem struct {
	Path     string
	Name     string
	IsParent bool
}

// listDrives 列出所有可用驱动器（仅 Windows）
func listDrives() []DirItem {
	var drives []DirItem
	
	// 只在 Windows 上执行
	if runtime.GOOS != "windows" {
		return drives
	}
	
	// 检测 A-Z 所有可能的驱动器
	for _, drive := range "ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
		drivePath := string(drive) + ":\\"
		if _, err := os.Stat(drivePath); err == nil {
			drives = append(drives, DirItem{
				Path:     drivePath,
				Name:     string(drive) + ":",
				IsParent: false,
			})
		}
	}
	
	return drives
}

// isRootPath 判断是否是根路径（跨平台）
func isRootPath(path string) bool {
	switch runtime.GOOS {
	case "windows":
		// Windows: 判断是否是盘符根（C:\, D:\ 等）
		if path == "" {
			return true  // 空字符串代表驱动器列表层
		}
		return filepath.VolumeName(path)+"\\" == path
	default:
		// Unix-like: 判断是否是 /
		return path == "/" || path == ""
	}
}

// getParentPath 获取父路径（跨平台）
func getParentPath(path string) string {
	if runtime.GOOS == "windows" {
		// Windows: 如果是盘符根，返回驱动器列表
		if filepath.VolumeName(path)+"\\" == path {
			return ""  // 空字符串 = 显示驱动器列表
		}
	}
	
	// 其他情况返回父目录
	return filepath.Dir(path)
}

// showCustomFolderDialog 显示类似 Windows 资源管理器风格的目录浏览窗口（独立窗口）
func (l *GVALauncher) showCustomFolderDialog() {
	// 获取初始路径（跨平台）
	selectedPath := getInitialBrowsePath(l.config.GVARootPath)
	
	// 创建独立窗口
	browseWindow := fyne.CurrentApp().NewWindow("📂 浏览文件夹")
	
	// 创建路径输入框（显示当前路径 + 可手动输入）
	pathInput := widget.NewEntry()
	pathInput.SetPlaceHolder("输入或粘贴路径，按回车或点击跳转")
	pathInput.SetText(selectedPath)  // 初始显示当前路径
	
	// 状态标签
	statusLabel := widget.NewLabel("")
	
	// 存储所有目录项
	var currentDirs []DirItem
	
	// 勾选的目录（用于确认时提交）
	var checkedPath string
	var checkedID widget.ListItemID = -1
	
	// 目录列表
	var dirList *widget.List
	
	// 更新目录列表
	updateDirList := func(path string) {
		currentDirs = []DirItem{}
		// 清除勾选状态（切换目录时）
		checkedPath = ""
		checkedID = -1
		
		// Windows 特殊处理：空路径 = 显示驱动器列表
		if runtime.GOOS == "windows" && path == "" {
			drives := listDrives()
			for _, drive := range drives {
				currentDirs = append(currentDirs, DirItem{
					Path:     drive.Path,
					Name:     "💿 " + drive.Name,
					IsParent: false,
				})
			}
		selectedPath = ""
		pathInput.SetText("💿 选择驱动器")
		statusLabel.SetText("")  // 删除数量显示
			if dirList != nil {
				dirList.Refresh()
			}
			return
		}
		
		selectedPath = path
		pathInput.SetText(path)  // 更新输入框显示当前路径
		
		// 添加"返回上级"或"返回驱动器列表"选项
		if runtime.GOOS == "windows" {
			// Windows: 如果是磁盘根目录（C:\, D:\ 等），显示"返回驱动器列表"
			if isRootPath(path) {
				currentDirs = append(currentDirs, DirItem{
					Path:     "",  // 空字符串代表驱动器列表
					Name:     "⬆️ 返回驱动器列表",
					IsParent: true,
				})
			} else {
				// 非根目录，显示"返回上级"
				parentPath := getParentPath(path)
				currentDirs = append(currentDirs, DirItem{
					Path:     parentPath,
					Name:     "⬆️ 返回上级",
					IsParent: true,
				})
			}
		} else {
			// Unix-like: 如果不是根目录 /，添加"返回上级"
			if !isRootPath(path) {
				parentPath := getParentPath(path)
				currentDirs = append(currentDirs, DirItem{
					Path:     parentPath,
					Name:     "⬆️ 返回上级",
					IsParent: true,
				})
			}
		}
		
		// 读取目录
		files, err := ioutil.ReadDir(path)
		if err != nil {
			statusLabel.SetText("❌ 无法读取目录")
			if dirList != nil {
				dirList.Refresh()
			}
			return
		}
		
		// 只显示文件夹
		count := 0
		for _, f := range files {
			if f.IsDir() {
				currentDirs = append(currentDirs, DirItem{
					Path:     filepath.Join(path, f.Name()),
					Name:     "📁 " + f.Name(),
					IsParent: false,
				})
				count++
			}
		}
		
		// 检查是否是GVA目录
		serverPath := filepath.Join(path, "server")
		webPath := filepath.Join(path, "web")
		
		if l.dirExists(serverPath) && l.dirExists(webPath) {
			statusLabel.SetText("✅ 有效的 GVA 项目")
		} else {
			statusLabel.SetText("")  // 删除数量显示
		}
		
		if dirList != nil {
			dirList.Refresh()
		}
	}
	
	// 创建目录列表
	dirList = widget.NewList(
		func() int {
			return len(currentDirs)
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("")
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id >= len(currentDirs) {
				return
			}
			
			label := obj.(*widget.Label)
			item := currentDirs[id]
			
			// 显示名称，如果是勾选的项则在后面添加绿色勾
			if id == checkedID {
				label.SetText(item.Name + " ✅")
			} else {
				label.SetText(item.Name)
			}
		},
	)
	
	// 双击进入目录
	var lastClickTime time.Time
	var lastClickID widget.ListItemID = -1
	
	dirList.OnSelected = func(id widget.ListItemID) {
		if id >= len(currentDirs) {
			return
		}
		
		now := time.Now()
		item := currentDirs[id]
		
		// 如果是"返回上级"，单击即可进入
		if item.IsParent {
			// 单击返回上级
			updateDirList(item.Path)
			selectedPath = item.Path
			// 清除勾选状态
			checkedPath = ""
			checkedID = -1
			dirList.UnselectAll()
			return
		}
		
		// 点击检测
		
		// 检测双击（500ms内点击同一项）
		if id == lastClickID && now.Sub(lastClickTime) < 500*time.Millisecond && lastClickTime.UnixNano() > 0 {
			// 双击：进入目录
				// 双击进入目录
			updateDirList(item.Path)
			selectedPath = item.Path
			// 清除勾选状态
			checkedPath = ""
			checkedID = -1
			lastClickID = -1
			// 立即取消选中，使下次点击能触发 OnSelected
			dirList.UnselectAll()
		} else {
			// 单击：切换勾选状态
				// 单击文件
			
			if checkedID == id {
				// 再次单击同一项：取消勾选
				// 取消勾选
				checkedPath = ""
				checkedID = -1
			} else {
				// 单击其他项：勾选新项
				// 勾选文件
				checkedPath = item.Path
				checkedID = id
			}
			
			selectedPath = item.Path
			lastClickID = id
			lastClickTime = now
			
			// 刷新列表显示勾选状态
			dirList.Refresh()
			
			// 关键修复：立即取消选中，让下次点击能触发 OnSelected
			// 使用 goroutine 延迟执行，避免影响当前选中效果
			go func() {
				time.Sleep(50 * time.Millisecond)
				dirList.UnselectAll()
			}()
		}
	}
	
	// 跳转到输入的路径
	jumpToPath := func() {
		inputPath := strings.TrimSpace(pathInput.Text)
		if inputPath == "" {
			return
		}
		
		// 检查路径是否存在
		if _, err := os.Stat(inputPath); err != nil {
			statusLabel.SetText("❌ 路径不存在或无法访问")
			return
		}
		
		// 展开该目录（updateDirList 会自动更新 pathInput）
		updateDirList(inputPath)
		selectedPath = inputPath
		// 不清空输入框，让 updateDirList 更新显示
	}
	
	// 跳转按钮
	jumpBtn := widget.NewButton("　🔍 跳转　", func() {
		jumpToPath()
	})
	
	// 回车键跳转
	pathInput.OnSubmitted = func(text string) {
		jumpToPath()
	}
	
	// 确认按钮
	confirmBtn := widget.NewButton("✅ 确认", func() {
		// 优先使用勾选的路径，如果没有勾选则使用输入框路径
		var finalPath string
		if checkedPath != "" {
			// 有勾选的目录，使用勾选的
			finalPath = checkedPath
			// 提交勾选的路径
		} else {
			// 没有勾选，使用输入框路径
			finalPath = strings.TrimSpace(pathInput.Text)
			// 提交输入框路径
		}
		
		if finalPath == "" {
			dialog.ShowError(fmt.Errorf("请选择一个文件夹"), browseWindow)
			return
		}
		
		if !l.dirExists(finalPath) {
			dialog.ShowError(fmt.Errorf("所选文件夹不存在"), browseWindow)
			return
		}
		
		// ============ 优先级1：检查是否是同一个路径 ============
		if finalPath == l.config.GVARootPath {
			// 路径没有变化，直接关闭窗口，不做任何操作
			browseWindow.Close()
			return
		}
		
		// ============ 路径发生了变化，需要处理 ============
		
		// 优先级2：记录旧状态（在修改路径之前）
		oldBackendPort := l.backendPort
		oldFrontendPort := l.frontendPort
		wasRunning := l.backendService.IsRunning || l.frontendService.IsRunning
		
		// 优先级3：立即更新路径
		l.gvaPathEntry.SetText(finalPath)
		l.config.GVARootPath = finalPath
		
		// 优先级4：立即读取新路径的端口配置（同步执行）
		l.updatePortsFromGVAConfig()
		// 注意：如果新路径是错误路径，updatePortsFromGVAConfig会将端口设为0
		
		// 优先级5：停止旧端口的服务（无论新路径是否正确）
		if wasRunning {
			// 使用旧端口号停止服务
			if oldBackendPort > 0 {
				l.killProcessByPort(oldBackendPort)
			}
			if oldFrontendPort > 0 {
				l.killProcessByPort(oldFrontendPort)
			}
			
			// 清理服务状态
			l.backendService.IsRunning = false
			l.backendService.Process = nil
			l.frontendService.IsRunning = false
			l.frontendService.Process = nil
			
			// 更新UI显示
			l.startButton.Enable()
			l.stopButton.Disable()
			l.updateServiceStatus()
			
			// 等待服务停止
			time.Sleep(500 * time.Millisecond)
		}
		
		// 优先级6：后台加载其他配置
		go func() {
			// 并发加载镜像源和Redis配置
			var wg sync.WaitGroup
			wg.Add(2)
			
			go func() {
				defer wg.Done()
				l.loadMirrorConfig()
			}()
			
			go func() {
				defer wg.Done()
				l.loadRedisConfig()
			}()
			
			wg.Wait()
			
			// 检查依赖
			l.checkDependencies()
			
			// 保存配置
			err := l.saveConfig()
			if err != nil {
				fyne.Do(func() {
					dialog.ShowError(fmt.Errorf("保存配置失败: %v", err), browseWindow)
				})
				return
			}
			
			// 关闭浏览窗口并显示提示
			fyne.Do(func() {
				if wasRunning {
					// 根据新路径是否有效显示不同提示
					var message string
					if l.backendPort > 0 && l.frontendPort > 0 {
						// 新路径有效
						message = fmt.Sprintf("GVA目录已更新\n\n旧端口服务已自动关闭:\n• 后端: %d\n• 前端: %d\n\n新端口:\n• 后端: %d\n• 前端: %d", 
							oldBackendPort, oldFrontendPort, l.backendPort, l.frontendPort)
					} else {
						// 新路径无效
						message = fmt.Sprintf("GVA目录已更新\n\n旧端口服务已自动关闭:\n• 后端: %d\n• 前端: %d\n\n⚠️ 新路径配置读取失败，请检查目录是否正确", 
							oldBackendPort, oldFrontendPort)
					}
					dialog.ShowInformation("提示", message, browseWindow)
				}
				browseWindow.Close()
			})
		}()
	})
	
	// 取消按钮
	cancelBtn := widget.NewButton("❌ 取消", func() {
		browseWindow.Close()
	})
	
	// 按钮容器
	buttons := container.NewGridWithColumns(2, confirmBtn, cancelBtn)
	
	// 路径输入行：标签 + 输入框 + 按钮
	pathRow := container.NewBorder(
		nil, nil,
		widget.NewLabel("当前路径:"),
		jumpBtn,
		pathInput,
	)
	
	// 窗口内容
	content := container.NewBorder(
		container.NewVBox(
			widget.NewLabelWithStyle("📂 选择 GVA 根目录", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
			widget.NewSeparator(),
			pathRow,
			statusLabel,
			widget.NewSeparator(),
		),
		buttons,
		nil,
		nil,
		dirList,
	)
	
	// 初始化目录列表
	updateDirList(selectedPath)
	
	browseWindow.SetContent(content)
	
	// 设置窗口大小（屏幕分辨率的一半）
	// 保护逻辑：确保屏幕尺寸有效
	if l.screenWidth <= 0 || l.screenHeight <= 0 {
		// 1. 尝试从配置文件重新加载
		l.loadConfig()
		
		// 2. 如果还是无效，重新检测屏幕
		if l.screenWidth <= 0 || l.screenHeight <= 0 {
			l.detectScreenSize()
			l.windowWidth = l.screenWidth * 0.42
			l.windowHeight = l.screenHeight * 0.89
		}
	}
	
	windowWidth := l.screenWidth / 2    // 屏幕宽度的一半
	windowHeight := l.screenHeight / 2  // 屏幕高度的一半
	
	// 浏览窗口尺寸已计算
	
	// 设置固定大小（防止内容自动扩展窗口）
	browseWindow.SetFixedSize(true)
	browseWindow.Resize(fyne.NewSize(windowWidth, windowHeight))
	browseWindow.CenterOnScreen()
	browseWindow.Show()
}

// getAllDependencies 从go.mod文件中读取所有依赖（包名@版本号格式）
func (l *GVALauncher) getAllDependencies() ([]string, error) {
	goModPath := filepath.Join(l.config.GVARootPath, "server", "go.mod")
	
	content, err := os.ReadFile(goModPath)
	if err != nil {
		return nil, fmt.Errorf("无法读取go.mod文件: %v", err)
	}
	
	var dependencies []string
	lines := strings.Split(string(content), "\n")
	inRequireBlock := false
	
	for _, line := range lines {
		line = strings.TrimSpace(line)
		
		// 检查是否进入require块
		if strings.HasPrefix(line, "require (") {
			inRequireBlock = true
			continue
		}
		
		// 检查是否退出require块
		if inRequireBlock && line == ")" {
			break
		}
		
		// 在require块中，解析依赖
		if inRequireBlock && line != "" && !strings.HasPrefix(line, "//") {
			// 保留包名和版本号，构建完整的模块标识
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				packageName := parts[0]  // 包名
				version := parts[1]      // 版本号
				
				// 只过滤掉本地替换，保留所有依赖（包括indirect）
				if !strings.HasPrefix(packageName, "./") && !strings.HasPrefix(packageName, "../") {
					// 构建完整的模块标识：包名@版本号
					fullModule := packageName + "@" + version
					dependencies = append(dependencies, fullModule)
				}
			}
		}
		
		// 处理单行require
		if strings.HasPrefix(line, "require ") && !strings.Contains(line, "(") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				packageName := parts[1]  // 包名
				version := parts[2]      // 版本号
				
				if !strings.HasPrefix(packageName, "./") && !strings.HasPrefix(packageName, "../") {
					// 构建完整的模块标识：包名@版本号
					fullModule := packageName + "@" + version
					dependencies = append(dependencies, fullModule)
				}
			}
		}
	}
	
	return dependencies, nil
}

// buildDependencyMap 根据依赖列表构建检测映射
func (l *GVALauncher) buildDependencyMap(dependencies []string) map[string][]string {
	depMap := make(map[string][]string)
	
	for _, dep := range dependencies {
		// 为每个依赖生成可能的缓存路径
		paths := []string{dep}
		
		// 添加父路径（如github.com/gin-gonic/gin -> github.com/gin-gonic）
		parts := strings.Split(dep, "/")
		if len(parts) >= 2 {
			parentPath := strings.Join(parts[:len(parts)-1], "/")
			paths = append(paths, parentPath)
		}
		
		// 添加域名路径（如github.com/gin-gonic/gin -> github.com）
		if len(parts) >= 3 {
			domainPath := parts[0]
			paths = append(paths, domainPath)
		}
		
		depMap[dep] = paths
	}
	
	return depMap
}

// encodeModulePath 将模块路径编码为Go缓存路径格式
func (l *GVALauncher) encodeModulePath(modulePath string) string {
	// Go模块缓存中，大写字母会被编码为 !小写字母
	// 例如：github.com/Masterminds/semver -> github.com/!masterminds/semver
	encoded := ""
	for _, char := range modulePath {
		if char >= 'A' && char <= 'Z' {
			encoded += "!" + string(char-'A'+'a')
		} else {
			encoded += string(char)
		}
	}
	return encoded
}

// checkBackendDependenciesInstalled 统一的后端依赖检测函数
func (l *GVALauncher) checkBackendDependenciesInstalled() bool {
	// 检查后端依赖：go.mod 和 go.sum 配置文件存在 + 缓存检测
	goModPath := filepath.Join(l.config.GVARootPath, "server", "go.mod")
	goSumPath := filepath.Join(l.config.GVARootPath, "server", "go.sum")
	backendConfigExists := l.fileExists(goModPath) && l.fileExists(goSumPath)

	if !backendConfigExists {
		return false
	}

	// 使用安全的方法检测依赖（不触发下载）
	// 1. 获取 Go 模块缓存路径
	modCache, err := l.getGoModCache()
	if err != nil {
		// 无法获取Go模块缓存路径
		return false
	}

	// Go模块缓存路径已获取

	// 2. 从配置文件读取所有依赖包名
	// 从go.mod读取所有依赖
	
	allDeps, err := l.getAllDependencies()
	if err != nil {
		// 无法读取依赖
		return false
	}
	
	// 找到直接依赖

	// 3. 并发检查每个依赖包是否在缓存中存在（精确匹配 包名@版本号）
	var mu sync.Mutex
	var wg sync.WaitGroup
	existCount := 0
	totalCount := len(allDeps)
	
	// 使用信号量限制并发数为20（避免打开过多文件句柄）
	semaphore := make(chan struct{}, 20)

	for _, fullModule := range allDeps {
		wg.Add(1)
		go func(module string) {
			defer wg.Done()
			
			// 获取信号量
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			
			// 将完整模块标识转换为缓存路径（处理大小写编码）
			// module 格式: github.com/gin-gonic/gin@v1.10.0
			cachePath := l.encodeModulePath(module)
			fullPath := filepath.Join(modCache, cachePath)
			
			// 直接检查精确的 包名@版本号 路径是否存在
			if l.dirExists(fullPath) {
				mu.Lock()
				existCount++
				mu.Unlock()
			}
		}(fullModule)
	}
	
	// 等待所有检查完成
	wg.Wait()

	// 判断依赖是否完整（90% 的依赖存在即认为已安装）
	threshold := totalCount * 90 / 100
	if threshold < 1 {
		threshold = 1
	}

	backendExists := existCount >= threshold
	// 后端依赖检测完成

	return backendExists
}

// checkDependencies 检查依赖状态
func (l *GVALauncher) checkDependencies() {
	if l.config.GVARootPath == "" {
		fyne.Do(func() {
			l.depStatusLabel.SetText("⚪ 未检测")
			l.frontendDepLabel.SetText("　　• 请先指定 GVA 根目录")
			l.backendDepLabel.SetText("")
			l.checkDepsButton.Disable()
			l.installDepsButton.Disable()
		})
		return
	}
	
	fyne.Do(func() {
		l.checkDepsButton.Enable()
		l.installDepsButton.Enable()
	})
	
	// 并发检查前后端依赖
	var wg sync.WaitGroup
	var frontendExists, backendExists bool
	
	wg.Add(2)
	
	// 任务1: 检查前端依赖
	go func() {
		defer wg.Done()
		
		// 检查前端依赖：package.json 配置文件存在 + node_modules 目录存在 + 验证依赖完整性
		packageJsonPath := filepath.Join(l.config.GVARootPath, "web", "package.json")
		nodeModulesPath := filepath.Join(l.config.GVARootPath, "web", "node_modules")
		frontendConfigExists := l.fileExists(packageJsonPath) && l.dirExists(nodeModulesPath)
		
		if frontendConfigExists {
			// 配置文件和 node_modules 都存在，验证依赖是否完整
			webPath := filepath.Join(l.config.GVARootPath, "web")
			cmd := createHiddenCmd("npm", "ls", "--depth=0")
			cmd.Dir = webPath
			err := cmd.Run()
			// npm ls 返回 0 表示所有依赖都已安装
			frontendExists = (err == nil)
		} else {
			frontendExists = false
		}
	}()
	
	// 任务2: 检查后端依赖
	go func() {
		defer wg.Done()
		backendExists = l.checkBackendDependenciesInstalled()
	}()
	
	// 等待两个检查都完成
	wg.Wait()
	
	// 更新显示（确保在主线程中执行）
	fyne.Do(func() {
		if frontendExists && backendExists {
			l.depStatusLabel.SetText("✅ 配置正常")
			l.frontendDepLabel.SetText("　　• ✅ 前端依赖已安装")
			l.backendDepLabel.SetText("　　• ✅ 后端依赖已安装")
		} else if !frontendExists && !backendExists {
			l.depStatusLabel.SetText("❌ 依赖缺失")
			l.frontendDepLabel.SetText("　　• ❌ 前端依赖未安装")
			l.backendDepLabel.SetText("　　• ❌ 后端依赖未安装")
		} else if frontendExists {
			l.depStatusLabel.SetText("⚠️ 依赖部分缺失")
			l.frontendDepLabel.SetText("　　• ✅ 前端依赖已安装")
			l.backendDepLabel.SetText("　　• ❌ 后端依赖未安装")
		} else {
			l.depStatusLabel.SetText("⚠️ 依赖部分缺失")
			l.frontendDepLabel.SetText("　　• ❌ 前端依赖未安装")
			l.backendDepLabel.SetText("　　• ✅ 后端依赖已安装")
		}
	})
}

// installDependencies 安装依赖
func (l *GVALauncher) installDependencies() {
	if l.config.GVARootPath == "" {
		dialog.ShowError(fmt.Errorf("请先指定 GVA 根目录"), l.window)
		return
	}
	
	progress := dialog.NewProgressInfinite("安装依赖", "正在安装依赖，请稍候...", l.window)
	progress.Show()
	
	go func() {
		var wg sync.WaitGroup
		var mu sync.Mutex
		var errors []string
		var frontendExists, backendExists bool
		
		// 阶段1: 并发检查前后端依赖状态
		wg.Add(2)
		
		// 任务1: 检查前端依赖
		go func() {
			defer wg.Done()
			
			packageJsonPath := filepath.Join(l.config.GVARootPath, "web", "package.json")
			nodeModulesPath := filepath.Join(l.config.GVARootPath, "web", "node_modules")
			frontendConfigExists := l.fileExists(packageJsonPath) && l.dirExists(nodeModulesPath)
			
			if frontendConfigExists {
				webPath := filepath.Join(l.config.GVARootPath, "web")
				cmd := createHiddenCmd("npm", "ls", "--depth=0")
				cmd.Dir = webPath
				err := cmd.Run()
				frontendExists = (err == nil)
			} else {
				frontendExists = false
			}
		}()
		
		// 任务2: 检查后端依赖
		go func() {
			defer wg.Done()
			backendExists = l.checkBackendDependenciesInstalled()
		}()
		
		// 等待检查完成
		wg.Wait()
		
		// 阶段2: 并发安装前后端依赖
		wg.Add(2)
		
		// 任务1: 安装前端依赖
		go func() {
			defer wg.Done()
			if !frontendExists {
				err := l.installFrontendDeps()
				if err != nil {
					mu.Lock()
					errors = append(errors, "前端: "+err.Error())
					mu.Unlock()
				}
			}
		}()
		
		// 任务2: 安装后端依赖
		go func() {
			defer wg.Done()
			if !backendExists {
				err := l.installBackendDeps()
				if err != nil {
					mu.Lock()
					errors = append(errors, "后端: "+err.Error())
					mu.Unlock()
				}
			}
		}()
		
		// 等待安装完成
		wg.Wait()
		
		// 在主线程中更新UI
		fyne.Do(func() {
			progress.Hide()
			
			if len(errors) > 0 {
				dialog.ShowError(fmt.Errorf("安装失败:\n%s", strings.Join(errors, "\n")), l.window)
			} else {
				dialog.ShowInformation("成功", "依赖安装完成", l.window)
			}
		})
		
		l.checkDependencies()
	}()
}

// installFrontendDeps 安装前端依赖
func (l *GVALauncher) installFrontendDeps() error {
	webPath := filepath.Join(l.config.GVARootPath, "web")
	// 前端依赖安装开始
	
	// 从界面输入框读取镜像源地址
	mirrorURL := ""
	if l.frontendMirrorEntry != nil {
		mirrorURL = strings.TrimSpace(l.frontendMirrorEntry.Text)
	}
	
	// 如果设置了镜像源，先设置 npm registry
	if mirrorURL != "" {
		// 设置前端镜像源
		cmd := createHiddenCmd("npm", "config", "set", "registry", mirrorURL)
		cmd.Dir = webPath
		if err := cmd.Run(); err != nil {
			// 设置镜像源失败
			return fmt.Errorf("设置 npm 镜像源失败: %v", err)
		}
		// 镜像源设置成功
	} else {
		// 使用默认前端镜像源
	}
	
	// 安装依赖
	// 执行npm install
	cmd := createHiddenCmd("npm", "install")
	cmd.Dir = webPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		// 前端依赖安装失败
		// 输出信息已获取
		return fmt.Errorf("npm install 失败: %v\n%s", err, string(output))
	}
	
	// 前端依赖安装成功
	// npm install输出已获取
	return nil
}

// installBackendDeps 安装后端依赖
func (l *GVALauncher) installBackendDeps() error {
	serverPath := filepath.Join(l.config.GVARootPath, "server")
	// 后端依赖安装开始
	
	// 从界面输入框读取代理地址
	proxyURL := ""
	if l.backendMirrorEntry != nil {
		proxyURL = strings.TrimSpace(l.backendMirrorEntry.Text)
	}
	
	// 如果设置了代理，先设置 GOPROXY
	if proxyURL != "" {
		// 设置GOPROXY
		cmd := createHiddenCmd("go", "env", "-w", "GOPROXY="+proxyURL)
		if err := cmd.Run(); err != nil {
			// 设置GOPROXY失败
			return fmt.Errorf("设置 GOPROXY 失败: %v", err)
		}
		// GOPROXY设置成功
	} else {
		// 使用默认GOPROXY
	}
	
	// 先列出需要下载的依赖
	// 检查需要下载的依赖
	listCmd := createHiddenCmd("go", "list", "-m", "all")
	listCmd.Dir = serverPath
	listOutput, err := listCmd.Output()
	if err != nil {
		// 无法列出依赖
	} else {
		lines := strings.Split(string(listOutput), "\n")
		// 发现依赖
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "github.com/flipped-aurora/gin-vue-admin/server") {
				// 依赖列表项
			}
		}
	}
	
	// 下载依赖
	// 执行go mod download
	cmd := createHiddenCmd("go", "mod", "download")
	cmd.Dir = serverPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		// 后端依赖安装失败
		// 输出信息已获取
		return fmt.Errorf("go mod download 失败: %v\n%s", err, string(output))
	}
	
	// 后端依赖安装成功
	if string(output) != "" {
		// go mod download输出已获取
	} else {
		// go mod download完成
	}
	return nil
}

// startGVA 启动 GVA 服务
func (l *GVALauncher) startGVA() {
	if l.config.GVARootPath == "" {
		// 未指定GVA根目录
		dialog.ShowError(fmt.Errorf("请先指定 GVA 根目录"), l.window)
		return
	}
	
	// 开始启动GVA服务
	// GVA根目录已设置
	// 后端端口已设置
	// 前端端口已设置
	
	l.startButton.Disable()
	l.stopButton.Enable()
	
	// 启动后端
	// 启动后端服务
	go l.startBackend()
	
	// 在 goroutine 中等待 2 秒后启动前端（避免阻塞 UI）
	go func() {
		// 等待后启动前端
		time.Sleep(2 * time.Second)
		l.startFrontend()
	}()
	
	// 启动状态监控（每秒更新一次）
	// 启动状态监控
	go l.startStatusMonitor()
}

// startBackend 启动后端服务（代码式启动）
func (l *GVALauncher) startBackend() {
	serverPath := filepath.Join(l.config.GVARootPath, "server")
	// 后端工作目录已设置
	
	// 代码式启动：直接在 goroutine 中运行 GVA 后端
	go func() {
		// 切换到服务器目录
		originalDir, _ := os.Getwd()
		defer os.Chdir(originalDir)
		
		err := os.Chdir(serverPath)
		if err != nil {
			// 切换目录失败
			return
		}
		
		// 开始后端代码式启动
		
		// 调用 GVA 的启动函数
		l.runGVABackend()
	}()
	
	// 等待一下让服务启动
	time.Sleep(1 * time.Second)
	
		// 启动后端成功
	// 后端端口已设置
	
	l.backendService.IsRunning = true
	l.backendService.Port = l.backendPort
	l.backendService.StartTime = time.Now()
	l.backendService.Process = nil // 代码式启动没有独立进程
}

// runGVABackend 运行 GVA 后端服务（代码式）
func (l *GVALauncher) runGVABackend() {
	defer func() {
		if r := recover(); r != nil {
			// 后端服务崩溃
			l.backendService.IsRunning = false
		}
	}()
	
	// 这里需要导入并调用 GVA 的初始化和启动函数
	// 由于直接导入会有依赖冲突，我们使用 plugin 方式或者 exec 方式
	// 暂时使用改进的 exec 方式，但不显示控制台窗口
	
	// 执行GVA主程序
	
	cmd := exec.Command("go", "run", "main.go")
	cmd.Dir = "."  // 当前目录已经是 server 目录
	cmd.Env = os.Environ()
	
	// 不显示控制台窗口，但捕获输出
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			HideWindow: true,
		}
	}
	
	// 启动服务
	err := cmd.Start()
	if err != nil {
		// 代码式启动失败
		l.backendService.IsRunning = false
		return
	}
	
		// 代码式启动成功
	l.backendService.Process = cmd.Process
	
	// 等待进程结束
	cmd.Wait()
	// 后端服务已停止
	l.backendService.IsRunning = false
}

// startFrontend 启动前端服务（代码式启动）
func (l *GVALauncher) startFrontend() {
	webPath := filepath.Join(l.config.GVARootPath, "web")
	// 前端工作目录已设置
	
	// 代码式启动：直接在 goroutine 中运行前端服务
	go func() {
		// 切换到前端目录
		originalDir, _ := os.Getwd()
		defer os.Chdir(originalDir)
		
		err := os.Chdir(webPath)
		if err != nil {
			// 切换前端目录失败
			return
		}
		
		// 开始前端代码式启动
		
		// 调用前端启动函数
		l.runVueFrontend()
	}()
	
	// 等待一下让服务启动
	time.Sleep(2 * time.Second)
	
		// 启动前端成功
	// 前端端口已设置
	
	l.frontendService.IsRunning = true
	l.frontendService.Port = l.frontendPort
	l.frontendService.StartTime = time.Now()
	l.frontendService.Process = nil // 代码式启动没有独立进程
}

// runVueFrontend 运行 Vue 前端服务（代码式）
func (l *GVALauncher) runVueFrontend() {
	defer func() {
		if r := recover(); r != nil {
			// 前端服务崩溃
			l.frontendService.IsRunning = false
		}
	}()
	
	// 执行npm run serve
	
	cmd := exec.Command("npm", "run", "serve")
	cmd.Dir = "."  // 当前目录已经是 web 目录
	cmd.Env = os.Environ()
	
	// 不显示控制台窗口
	if runtime.GOOS == "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			HideWindow: true,
		}
	}
	
	// 启动服务
	err := cmd.Start()
	if err != nil {
		// 前端代码式启动失败
		l.frontendService.IsRunning = false
		return
	}
	
		// 前端代码式启动成功
	l.frontendService.Process = cmd.Process
	
	// 等待进程结束
	cmd.Wait()
	// 前端服务已停止
	l.frontendService.IsRunning = false
}

// stopGVA 停止 GVA 服务
func (l *GVALauncher) stopGVA() {
	// 开始停止GVA服务
	
	// 通过端口杀死进程（更可靠）
	if l.backendPort > 0 {
		// 停止后端服务
		l.killProcessByPort(l.backendPort)
	}
	
	if l.frontendPort > 0 {
		// 停止前端服务
		l.killProcessByPort(l.frontendPort)
	}
	
	// 清理进程信息
	// 清理进程信息
	l.backendService.IsRunning = false
	l.backendService.Process = nil
	
	l.frontendService.IsRunning = false
	l.frontendService.Process = nil
	
	l.startButton.Enable()
	l.stopButton.Disable()
	
	// 等待一下再更新状态
	// 等待后更新状态
	time.Sleep(500 * time.Millisecond)
	l.updateServiceStatus()
	// 服务停止完成
}

// killProcess 结束进程（包括子进程）
func (l *GVALauncher) killProcess(pid int) {
	if runtime.GOOS == "windows" {
		// /T 参数会杀死整个进程树（包括子进程）
		createHiddenCmd("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", pid)).Run()
	} else {
		exec.Command("kill", "-9", fmt.Sprintf("%d", pid)).Run()
	}
}

// killProcessByPort 通过端口号杀死占用该端口的进程
func (l *GVALauncher) killProcessByPort(port int) {
	// 查找占用端口的进程
	
	if runtime.GOOS == "windows" {
		// 使用 netstat 查找占用端口的进程 PID
		cmd := createHiddenCmd("cmd", "/C", fmt.Sprintf("netstat -ano | findstr :%d", port))
		output, err := cmd.Output()
		if err != nil {
			// netstat命令执行失败
			return
		}
		
		// netstat输出已获取
		
		// 解析输出，查找 PID
		lines := strings.Split(string(output), "\n")
		killedCount := 0
		for _, line := range lines {
			// 跳过空行
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			
			// 查找 LISTENING 状态的行
			if !strings.Contains(line, "LISTENING") {
				continue
			}
			
			// 提取 PID（最后一列）
			fields := strings.Fields(line)
			if len(fields) < 5 {
				continue
			}
			
			pidStr := fields[len(fields)-1]
			pid, err := strconv.Atoi(pidStr)
			if err != nil {
				continue
			}
			
			// 找到PID，执行taskkill
			// 杀死进程
			killCmd := createHiddenCmd("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", pid))
			killErr := killCmd.Run()
			if killErr != nil {
				// taskkill失败
			} else {
				// PID已终止
				killedCount++
			}
		}
		
		if killedCount == 0 {
			// 端口未找到占用进程
		} else {
			// 共终止进程
		}
	} else {
		// Linux/Mac: 使用 lsof
		// 使用lsof查找进程
		cmd := exec.Command("lsof", "-ti", fmt.Sprintf(":%d", port))
		output, err := cmd.Output()
		if err != nil {
			// lsof命令执行失败
			return
		}
		
		pidStr := strings.TrimSpace(string(output))
		if pidStr != "" {
			// 找到PID，执行kill
			killErr := exec.Command("kill", "-9", pidStr).Run()
			if killErr != nil {
				// kill失败
			} else {
				// PID已终止
			}
		} else {
			// 端口未找到占用进程
		}
	}
}

// updateServiceStatus 更新服务状态显示
func (l *GVALauncher) updateServiceStatus() {
	backendStatus := "🔴 已停止"
	frontendStatus := "🔴 已停止"
	
	if l.backendService.IsRunning {
		backendStatus = "✅ 运行中"
	}
	if l.frontendService.IsRunning {
		frontendStatus = "✅ 运行中"
	}
	
	// 显示端口信息
	backendPortStr := "未配置"
	if l.backendPort > 0 {
		backendPortStr = fmt.Sprintf("%d", l.backendPort)
	}
	
	frontendPortStr := "未配置"
	if l.frontendPort > 0 {
		frontendPortStr = fmt.Sprintf("%d", l.frontendPort)
	}
	
	// 使用 fyne.Do 确保 UI 更新在主线程中执行
	fyne.Do(func() {
		l.backendStatusLabel.SetText(fmt.Sprintf("　• 后端服务: %s 端口: %s", backendStatus, backendPortStr))
		l.frontendStatusLabel.SetText(fmt.Sprintf("　• 前端服务: %s 端口: %s", frontendStatus, frontendPortStr))
		
		// 更新访问地址 - 使用本机IP地址
		if l.frontendPort > 0 && l.config.GVARootPath != "" {
			localIP := l.getLocalIP()
			frontendURL := fmt.Sprintf("http://%s:%d", localIP, l.frontendPort)
			l.urlLabel.SetText("　• 前端: " + frontendURL)
		} else {
			l.urlLabel.SetText("　• 前端: 未配置")
		}
	})
}

// checkServiceStatus 检查服务状态
func (l *GVALauncher) checkServiceStatus() {
	// 从GVA配置文件读取端口
	l.updatePortsFromGVAConfig()
	
	// 检查后端端口
	l.backendService.IsRunning = l.isPortInUse(l.backendPort)
	
	// 检查前端端口
	l.frontendService.IsRunning = l.isPortInUse(l.frontendPort)
	
	l.updateServiceStatus()
	
	if l.backendService.IsRunning || l.frontendService.IsRunning {
		l.startButton.Disable()
		l.stopButton.Enable()
	} else {
		l.startButton.Enable()
		l.stopButton.Disable()
	}
}

// loadMirrorConfig 加载镜像源配置到输入框
func (l *GVALauncher) loadMirrorConfig() {
	if l.frontendMirrorEntry != nil {
		frontendMirror := l.readFrontendMirror()
		l.frontendMirrorEntry.SetText(frontendMirror)
	}
	
	if l.backendMirrorEntry != nil {
		backendMirror := l.readBackendMirror()
		l.backendMirrorEntry.SetText(backendMirror)
	}
}

// updatePortsFromGVAConfig 从GVA配置文件更新端口
func (l *GVALauncher) updatePortsFromGVAConfig() {
	if l.config.GVARootPath == "" {
		// 未设置目录，显示未配置
		l.backendPort = 0
		l.frontendPort = 0
		l.updateServiceStatus()
		return
	}
	
	gvaConfig, err := l.readGVAConfig()
	if err != nil {
		// 读取失败（选错目录），显示未配置
		l.backendPort = 0
		l.frontendPort = 0
		l.updateServiceStatus()
		return
	}
	
	// 更新后端端口
	if gvaConfig.System.Addr > 0 {
		l.backendPort = gvaConfig.System.Addr
	} else {
		l.backendPort = 0
	}
	
	// 从前端配置文件读取端口
	l.updateFrontendPortFromConfig()
	
	// 更新显示
	l.updateServiceStatus()
}

// updateFrontendPortFromConfig 从前端配置文件读取端口
func (l *GVALauncher) updateFrontendPortFromConfig() {
	// 默认端口
	l.frontendPort = 8080
	
	if l.config.GVARootPath == "" {
		return
	}
	
	webPath := filepath.Join(l.config.GVARootPath, "web")
	
	// 1. 优先从 .env.development 文件读取 VITE_CLI_PORT（这是我们修改的主要配置）
	envDevPath := filepath.Join(webPath, ".env.development")
	if data, err := ioutil.ReadFile(envDevPath); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "VITE_CLI_PORT=") {
				if port, err := strconv.Atoi(strings.TrimPrefix(line, "VITE_CLI_PORT=")); err == nil && port > 0 {
					l.frontendPort = port
					return
				}
			}
		}
	}
	
	// 2. 尝试从 .env 文件读取 PORT 或 VUE_APP_PORT
	envPath := filepath.Join(webPath, ".env")
	if data, err := ioutil.ReadFile(envPath); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "PORT=") {
				if port, err := strconv.Atoi(strings.TrimPrefix(line, "PORT=")); err == nil && port > 0 {
					l.frontendPort = port
					return
				}
			}
			if strings.HasPrefix(line, "VUE_APP_PORT=") {
				if port, err := strconv.Atoi(strings.TrimPrefix(line, "VUE_APP_PORT=")); err == nil && port > 0 {
					l.frontendPort = port
					return
				}
			}
		}
	}
	
	// 3. 尝试从 vue.config.js 读取 devServer.port
	vueConfigPath := filepath.Join(webPath, "vue.config.js")
	if data, err := ioutil.ReadFile(vueConfigPath); err == nil {
		content := string(data)
		// 简单的正则匹配查找 port: 数字
		if strings.Contains(content, "devServer") && strings.Contains(content, "port") {
			// 查找 port: 后面的数字
			lines := strings.Split(content, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.Contains(line, "port") && strings.Contains(line, ":") {
					// 提取端口号 (简单匹配，如: port: 8080, 或 port:8080)
					parts := strings.Split(line, ":")
					if len(parts) >= 2 {
						portStr := strings.TrimSpace(parts[1])
						portStr = strings.TrimSuffix(portStr, ",")
						portStr = strings.TrimSpace(portStr)
						if port, err := strconv.Atoi(portStr); err == nil && port > 0 {
							l.frontendPort = port
							return
						}
					}
				}
			}
		}
	}
	
	// 4. 尝试从 package.json 的 scripts.serve 读取 --port 参数
	packageJsonPath := filepath.Join(webPath, "package.json")
	if data, err := ioutil.ReadFile(packageJsonPath); err == nil {
		var pkg map[string]interface{}
		if err := json.Unmarshal(data, &pkg); err == nil {
			if scripts, ok := pkg["scripts"].(map[string]interface{}); ok {
				if serve, ok := scripts["serve"].(string); ok {
					// 查找 --port 参数
					if strings.Contains(serve, "--port") {
						parts := strings.Fields(serve)
						for i, part := range parts {
							if part == "--port" && i+1 < len(parts) {
								if port, err := strconv.Atoi(parts[i+1]); err == nil && port > 0 {
									l.frontendPort = port
									return
								}
							}
						}
					}
				}
			}
		}
	}
}

// showPortDialog 显示端口修改对话框
func (l *GVALauncher) showPortDialog(isBackend bool) {
	title := "修改前端端口"
	currentPort := l.frontendPort
	if isBackend {
		title = "修改后端端口"
		currentPort = l.backendPort
	}
	
	currentLabel := widget.NewLabel(fmt.Sprintf("当前端口: %d", currentPort))
	currentLabel.TextStyle = fyne.TextStyle{Bold: true}
	
	portEntry := widget.NewEntry()
	portEntry.SetPlaceHolder("输入新端口号...")
	
	statusLabel := widget.NewLabel("")
	statusLabel.Wrapping = fyne.TextWrapWord
	
	checkBtn := widget.NewButton("🔍 检查占用", func() {
		portStr := portEntry.Text
		if portStr == "" {
			statusLabel.SetText("⚠️ 请输入端口号")
			return
		}
		
		port, err := strconv.Atoi(portStr)
		if err != nil || port < 1 || port > 65535 {
			statusLabel.SetText("⚠️ 端口无效 (范围: 1-65535)")
			return
		}
		
		statusLabel.SetText("⏳ 正在检查端口占用情况...")
		
		go func() {
			time.Sleep(300 * time.Millisecond)
			if l.isPortInUse(port) {
				statusLabel.SetText(fmt.Sprintf("❌ 端口 %d 已被占用", port))
			} else {
				statusLabel.SetText(fmt.Sprintf("✅ 端口 %d 可用", port))
			}
		}()
	})
	
	portRow := container.NewBorder(nil, nil, widget.NewLabel("新端口:"), checkBtn, portEntry)
	
	content := container.NewVBox(
		currentLabel,
		widget.NewSeparator(),
		portRow,
		widget.NewSeparator(),
		statusLabel,
	)
	
	d := dialog.NewCustomConfirm(title, "确定", "取消", content, func(ok bool) {
		if !ok {
			return
		}
		
		// 记录当前端口（用于关闭旧服务）
		oldBackendPort := l.backendPort
		oldFrontendPort := l.frontendPort
		
		portStr := portEntry.Text
		port, err := strconv.Atoi(portStr)
		if err != nil || port < 1 || port > 65535 {
			dialog.ShowError(fmt.Errorf("端口号无效"), l.window)
			return
		}
		
		// 记录服务是否正在运行（用于提示信息）
		wasRunning := l.backendService.IsRunning || l.frontendService.IsRunning
		
		// 如果服务器正在运行，关闭整个服务
		if wasRunning {
			// 关闭前后端所有服务
			if oldBackendPort > 0 {
				l.killProcessByPort(oldBackendPort)
			}
			if oldFrontendPort > 0 {
				l.killProcessByPort(oldFrontendPort)
			}
			
			// 清理所有服务状态
			l.backendService.IsRunning = false
			l.frontendService.IsRunning = false
			l.backendService.Process = nil
			l.frontendService.Process = nil
			l.startButton.Enable()
			l.stopButton.Disable()
		}
		
		if isBackend {
			// 修改后端端口需要写入GVA配置文件
			err := l.writeGVAConfig(port)
			if err != nil {
				dialog.ShowError(fmt.Errorf("写入后端配置文件失败: %v", err), l.window)
				return
			}
			l.backendPort = port
		} else {
			// 修改前端端口需要特殊处理（避免Vue热重载导致的状态错误）
			
			// 1. 暂停状态监控
			l.pauseStatusMonitor = true
			
			// 2. 修改前端配置文件（会触发Vue热重载）
			err := l.writeFrontendConfig(port)
			if err != nil {
				l.pauseStatusMonitor = false // 出错时恢复状态监控
				dialog.ShowError(fmt.Errorf("写入前端配置文件失败: %v", err), l.window)
				return
			}
			l.frontendPort = port
			
			// 3. 后台处理Vue重启
			go func() {
				// 等待Vue重启完成（4秒通常够了）
				time.Sleep(4 * time.Second)
				
				// 杀死新启动的Vue进程
				l.killProcessByPort(port)
				
				// 等待进程完全停止
				time.Sleep(1 * time.Second)
				
				// 4. 恢复状态监控并更新界面
				fyne.Do(func() {
					l.frontendService.IsRunning = false
					l.pauseStatusMonitor = false
					l.updateServiceStatus()
				})
			}()
		}
		
		l.updateServiceStatus()
		
		// 根据服务状态显示不同的提示信息
		var message string
		if wasRunning {
			message = fmt.Sprintf("端口已修改为 %d\n\n服务已自动关闭，请重新启动", port)
		} else {
			message = fmt.Sprintf("端口已修改为 %d", port)
		}
		dialog.ShowInformation("成功", message, l.window)
	}, l.window)
	
	// ========================================
	// 【响应式对话框尺寸】使用 vw/vh 单位 - 正方形
	// ========================================
	// 对话框设置为正方形，使用窗口宽度的 45%
	dialogSize := l.calcVW(45)   // ⭐ 正方形尺寸
	d.Resize(fyne.NewSize(dialogSize, dialogSize))
	d.Show()
}

// isPortInUse 检查端口是否被占用
func (l *GVALauncher) isPortInUse(port int) bool {
	addr := fmt.Sprintf(":%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return true
	}
	listener.Close()
	return false
}

// getLocalIP 获取本机局域网IP地址（返回最后一个有效IP，避开VPN）
func (l *GVALauncher) getLocalIP() string {
	// 获取所有网络接口
	interfaces, err := net.Interfaces()
	if err != nil {
		return "localhost"
	}
	
	var validIPs []string
	
	// 遍历所有网络接口
	for _, iface := range interfaces {
		// 跳过未启用或回环接口
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		
		// 获取接口的地址
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
				if ipv4 := ipNet.IP.To4(); ipv4 != nil {
					ipStr := ipv4.String()
					
					// 跳过APIPA地址 (169.254.x.x)
					if strings.HasPrefix(ipStr, "169.254.") {
						continue
					}
					
					validIPs = append(validIPs, ipStr)
				}
			}
		}
	}
	
	// 返回最后一个有效IP（通常VPN和虚拟适配器在前面）
	if len(validIPs) > 0 {
		return validIPs[len(validIPs)-1]
	}
	
	return "localhost"
}

// fileExists 检查文件是否存在
func (l *GVALauncher) fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// dirExists 检查目录是否存在
func (l *GVALauncher) dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// ========================================
// Redis 配置管理
// ========================================

// loadRedisConfig 加载 Redis 配置到输入框
func (l *GVALauncher) loadRedisConfig() {
	if l.config.GVARootPath == "" {
		// 未设置目录，禁用所有 Redis 控件并清空内容
		l.updateRedisFieldsState(false)
		if l.redisSwitch != nil {
			l.redisSwitch.Disable()
			l.redisSwitch.SetChecked(false)
		}
		if l.redisAddrEntry != nil {
			l.redisAddrEntry.SetText("")
		}
		if l.redisPassEntry != nil {
			l.redisPassEntry.SetText("")
		}
		if l.redisDBEntry != nil {
			l.redisDBEntry.SetText("")
		}
		return
	}
	
	// 检查server目录是否存在
	serverPath := filepath.Join(l.config.GVARootPath, "server")
	if !l.dirExists(serverPath) {
		// server目录不存在，禁用所有 Redis 控件并清空内容
		l.updateRedisFieldsState(false)
		if l.redisSwitch != nil {
			l.redisSwitch.Disable()
			l.redisSwitch.SetChecked(false)
		}
		if l.redisAddrEntry != nil {
			l.redisAddrEntry.SetText("")
		}
		if l.redisPassEntry != nil {
			l.redisPassEntry.SetText("")
		}
		if l.redisDBEntry != nil {
			l.redisDBEntry.SetText("")
		}
		return
	}
	
	// 读取 GVA 配置
	gvaConfig, err := l.readGVAConfig()
	if err != nil {
		// 读取失败，禁用所有 Redis 控件并清空内容
		l.updateRedisFieldsState(false)
		if l.redisSwitch != nil {
			l.redisSwitch.Disable()
			l.redisSwitch.SetChecked(false)
		}
		if l.redisAddrEntry != nil {
			l.redisAddrEntry.SetText("")
		}
		if l.redisPassEntry != nil {
			l.redisPassEntry.SetText("")
		}
		if l.redisDBEntry != nil {
			l.redisDBEntry.SetText("")
		}
		return
	}
	
	// 启用 Redis 开关
	if l.redisSwitch != nil {
		l.redisSwitch.Enable()
		l.redisSwitch.SetChecked(gvaConfig.System.UseRedis)
	}
	
	// 加载 Redis 配置到输入框
	if l.redisAddrEntry != nil {
		l.redisAddrEntry.SetText(gvaConfig.Redis.Addr)
	}
	if l.redisPassEntry != nil {
		l.redisPassEntry.SetText(gvaConfig.Redis.Password)
	}
	if l.redisDBEntry != nil {
		l.redisDBEntry.SetText(fmt.Sprintf("%d", gvaConfig.Redis.DB))
	}
	
	// 缓存当前配置（用于取消操作）
	l.cachedRedisConfig.UseRedis = gvaConfig.System.UseRedis
	l.cachedRedisConfig.Addr = gvaConfig.Redis.Addr
	l.cachedRedisConfig.Password = gvaConfig.Redis.Password
	l.cachedRedisConfig.DB = gvaConfig.Redis.DB
	
	// 更新输入框状态
	l.updateRedisFieldsState(gvaConfig.System.UseRedis)
}

// saveRedisSwitch 立即保存 Redis 开关状态到配置文件（只写 use-redis 字段）
func (l *GVALauncher) saveRedisSwitch(useRedis bool) {
	if l.config.GVARootPath == "" {
		return  // 没有设置目录，静默返回
	}
	
	// 读取配置文件
	configPath := l.getGVAConfigPath()
	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		return  // 读取失败，静默返回
	}
	
	var gvaConfig map[string]interface{}
	err = yaml.Unmarshal(data, &gvaConfig)
	if err != nil {
		return  // 解析失败，静默返回
	}
	
	// 只更新 system.use-redis 字段
	if system, ok := gvaConfig["system"].(map[string]interface{}); ok {
		system["use-redis"] = useRedis
	} else {
		// 如果 system 不存在，创建它
		gvaConfig["system"] = map[string]interface{}{
			"use-redis": useRedis,
		}
	}
	
	// 写回文件
	newData, err := yaml.Marshal(gvaConfig)
	if err != nil {
		return  // 序列化失败，静默返回
	}
	
	err = ioutil.WriteFile(configPath, newData, 0644)
	if err != nil {
		return  // 写入失败，静默返回
	}
	
	// 更新缓存
	l.cachedRedisConfig.UseRedis = useRedis
}

// updateRedisFieldsState 更新 Redis 输入框和按钮的启用/禁用状态
func (l *GVALauncher) updateRedisFieldsState(enabled bool) {
	if l.redisAddrEntry != nil {
		if enabled {
			l.redisAddrEntry.Enable()
		} else {
			l.redisAddrEntry.Disable()
		}
	}
	
	if l.redisPassEntry != nil {
		if enabled {
			l.redisPassEntry.Enable()
		} else {
			l.redisPassEntry.Disable()
		}
	}
	
	if l.redisDBEntry != nil {
		if enabled {
			l.redisDBEntry.Enable()
		} else {
			l.redisDBEntry.Disable()
		}
	}
	
	if l.redisTestBtn != nil {
		if enabled {
			l.redisTestBtn.Enable()
		} else {
			l.redisTestBtn.Disable()
		}
	}
	
	if l.redisSaveBtn != nil {
		if enabled {
			l.redisSaveBtn.Enable()
		} else {
			l.redisSaveBtn.Disable()
		}
	}
	
	if l.redisCancelBtn != nil {
		if enabled {
			l.redisCancelBtn.Enable()
		} else {
			l.redisCancelBtn.Disable()
		}
	}
}

// saveRedisConfig 保存 Redis 配置到 config.yaml
func (l *GVALauncher) saveRedisConfig() {
	if l.config.GVARootPath == "" {
		dialog.ShowError(fmt.Errorf("请先指定 GVA 根目录"), l.window)
		return
	}
	
	// 验证数据库编号
	dbStr := strings.TrimSpace(l.redisDBEntry.Text)
	db, err := strconv.Atoi(dbStr)
	if err != nil || db < 0 || db > 15 {
		dialog.ShowError(fmt.Errorf("数据库编号无效，范围: 0-15"), l.window)
		return
	}
	
	// 记录服务是否正在运行（用于提示信息）
	wasRunning := l.backendService.IsRunning || l.frontendService.IsRunning
	
	// 如果服务器正在运行，先关闭前后端服务器
	if wasRunning {
		l.stopGVA()
	}
	
	// 读取配置文件
	configPath := l.getGVAConfigPath()
	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		dialog.ShowError(fmt.Errorf("读取配置文件失败: %v", err), l.window)
		return
	}
	
	var gvaConfig map[string]interface{}
	err = yaml.Unmarshal(data, &gvaConfig)
	if err != nil {
		dialog.ShowError(fmt.Errorf("解析配置文件失败: %v", err), l.window)
		return
	}
	
	// 更新 system.use-redis
	if system, ok := gvaConfig["system"].(map[string]interface{}); ok {
		system["use-redis"] = l.redisSwitch.Checked
	}
	
	// 更新 redis 配置
	if redis, ok := gvaConfig["redis"].(map[string]interface{}); ok {
		redis["addr"] = strings.TrimSpace(l.redisAddrEntry.Text)
		redis["password"] = l.redisPassEntry.Text
		redis["db"] = db
	} else {
		// 如果 redis 配置不存在，创建新的
		gvaConfig["redis"] = map[string]interface{}{
			"addr":     strings.TrimSpace(l.redisAddrEntry.Text),
			"password": l.redisPassEntry.Text,
			"db":       db,
		}
	}
	
	// 写回文件
	newData, err := yaml.Marshal(gvaConfig)
	if err != nil {
		dialog.ShowError(fmt.Errorf("序列化配置失败: %v", err), l.window)
		return
	}
	
	err = ioutil.WriteFile(configPath, newData, 0644)
	if err != nil {
		dialog.ShowError(fmt.Errorf("写入配置文件失败: %v", err), l.window)
		return
	}
	
	// 更新缓存
	l.cachedRedisConfig.UseRedis = l.redisSwitch.Checked
	l.cachedRedisConfig.Addr = strings.TrimSpace(l.redisAddrEntry.Text)
	l.cachedRedisConfig.Password = l.redisPassEntry.Text
	l.cachedRedisConfig.DB = db
	
	// 根据服务状态显示不同的提示信息
	var message string
	if wasRunning {
		message = "Redis 配置已保存\n\n服务已自动关闭，请重新启动"
	} else {
		message = "Redis 配置已保存"
	}
	dialog.ShowInformation("成功", message, l.window)
}

// cancelRedisConfig 取消 Redis 配置修改（恢复缓存的值）
func (l *GVALauncher) cancelRedisConfig() {
	// 恢复开关状态
	if l.redisSwitch != nil {
		l.redisSwitch.SetChecked(l.cachedRedisConfig.UseRedis)
	}
	
	// 恢复输入框内容
	if l.redisAddrEntry != nil {
		l.redisAddrEntry.SetText(l.cachedRedisConfig.Addr)
	}
	if l.redisPassEntry != nil {
		l.redisPassEntry.SetText(l.cachedRedisConfig.Password)
	}
	if l.redisDBEntry != nil {
		l.redisDBEntry.SetText(fmt.Sprintf("%d", l.cachedRedisConfig.DB))
	}
	
	// 更新输入框状态
	l.updateRedisFieldsState(l.cachedRedisConfig.UseRedis)
	
	dialog.ShowInformation("提示", "已恢复原配置", l.window)
}

// testRedisConnection 测试 Redis 连接（包含完整的认证和功能测试）
func (l *GVALauncher) testRedisConnection() {
	addr := strings.TrimSpace(l.redisAddrEntry.Text)
	password := l.redisPassEntry.Text
	dbStr := strings.TrimSpace(l.redisDBEntry.Text)
	
	if addr == "" {
		dialog.ShowError(fmt.Errorf("请输入 Redis 地址"), l.window)
		return
	}
	
	db, err := strconv.Atoi(dbStr)
	if err != nil || db < 0 || db > 15 {
		dialog.ShowError(fmt.Errorf("数据库编号无效，范围: 0-15"), l.window)
		return
	}
	
	// 显示进度对话框
	progress := dialog.NewProgressInfinite("测试连接", "正在进行详细的 Redis 连接测试...", l.window)
	progress.Show()
	
	go func() {
		
		var testResults []string
		
		// 1. TCP连接测试
		testResults = append(testResults, "🔍 步骤1: TCP连接测试")
		
		conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
		if err != nil {
			fyne.Do(func() {
				progress.Hide()
				dialog.ShowError(fmt.Errorf("❌ TCP连接失败: %v\n\n请检查:\n1. Redis 地址是否正确 (%s)\n2. Redis 服务是否启动\n3. 防火墙设置\n4. 网络连接", err, addr), l.window)
			})
			return
		}
		defer conn.Close()
		testResults = append(testResults, "✅ TCP连接成功")
		
		// 2. Redis协议握手测试
		testResults = append(testResults, "\n🔍 步骤2: Redis协议测试")
		
		// 设置读写超时
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		
		// 3. 统一密码认证测试（始终发送AUTH命令）
		testResults = append(testResults, "\n🔍 步骤3: Redis认证测试")
		
		// 发送 AUTH 命令（使用用户输入的密码，可能为空）
		var authCmd string
		if password == "" {
			authCmd = "AUTH \"\"\r\n" // 空密码用双引号包围
		} else {
			authCmd = fmt.Sprintf("AUTH %s\r\n", password)
		}
		_, err = conn.Write([]byte(authCmd))
		if err != nil {
			fyne.Do(func() {
				progress.Hide()
				dialog.ShowError(fmt.Errorf("❌ 发送认证命令失败: %v", err), l.window)
			})
			return
		}
		
		// 读取认证响应
		buffer := make([]byte, 1024)
		n, err := conn.Read(buffer)
		if err != nil {
			fyne.Do(func() {
				progress.Hide()
				dialog.ShowError(fmt.Errorf("❌ 认证响应超时: %v\n\n可能原因:\n1. Redis服务器无响应\n2. 网络连接问题", err), l.window)
			})
			return
		}
		
		response := strings.TrimSpace(string(buffer[:n]))
		if strings.HasPrefix(response, "+OK") {
			if password == "" {
				testResults = append(testResults, "✅ 认证成功（无密码模式）")
			} else {
				testResults = append(testResults, "✅ 密码认证成功")
			}
		} else if strings.Contains(response, "no password is set") {
			// Redis服务器没有设置密码，这是正常情况
			if password == "" {
				testResults = append(testResults, "✅ 认证成功（Redis无密码配置）")
			} else {
				// 用户输入了密码，但Redis没有设置密码
				fyne.Do(func() {
					progress.Hide()
					dialog.ShowError(fmt.Errorf("❌ Redis认证失败\n\nRedis服务器未设置密码，但您输入了密码\n\n请清空密码字段或在Redis服务器设置密码"), l.window)
				})
				return
			}
		} else {
			// 其他认证错误（密码错误等）
			fyne.Do(func() {
				progress.Hide()
				dialog.ShowError(fmt.Errorf("❌ Redis认证失败\n\n服务器响应: %s\n\n请检查密码是否与Redis服务器配置一致", response), l.window)
			})
			return
		}
		
		// 4. 数据库选择测试
		fmt.Println("🔍 [调试步骤22] 开始数据库选择测试")
		testResults = append(testResults, "\n🔍 步骤4: 数据库选择测试")
		if db != 0 {
			fmt.Printf("🔍 [调试步骤23] 选择数据库 %d\n", db)
			selectCmd := fmt.Sprintf("SELECT %d\r\n", db)
			_, err = conn.Write([]byte(selectCmd))
			if err != nil {
				fmt.Printf("❌ [调试步骤24] 发送数据库选择命令失败: %v\n", err)
				fyne.Do(func() {
					dialog.ShowError(fmt.Errorf("❌ 发送数据库选择命令失败: %v", err), l.window)
				})
				return
			}
			
			// 读取选择数据库响应
			buffer := make([]byte, 1024)
			fmt.Println("🔍 [调试步骤25] 等待SELECT响应...")
			n, err := conn.Read(buffer)
			if err != nil {
				fmt.Printf("❌ [调试步骤26] 读取数据库选择响应失败: %v\n", err)
				fyne.Do(func() {
					dialog.ShowError(fmt.Errorf("❌ 读取数据库选择响应失败: %v", err), l.window)
				})
				return
			}
			
			response := strings.TrimSpace(string(buffer[:n]))
			fmt.Printf("🔍 [调试步骤27] 收到SELECT响应: '%s'\n", response)
			if strings.HasPrefix(response, "+OK") {
				fmt.Printf("✅ [调试步骤28] 成功选择数据库 %d\n", db)
				testResults = append(testResults, fmt.Sprintf("✅ 成功选择数据库 %d", db))
			} else {
				fmt.Printf("❌ [调试步骤29] 数据库选择失败: %s\n", response)
				fyne.Do(func() {
					dialog.ShowError(fmt.Errorf("❌ 数据库选择失败\n\n服务器响应: %s\n\n请检查数据库编号 %d 是否有效", response, db), l.window)
				})
				return
			}
		} else {
			fmt.Println("🔍 [调试步骤23] 使用默认数据库 0，跳过SELECT命令")
			testResults = append(testResults, "✅ 使用默认数据库 0")
		}
		
		// 5. PING命令测试
		fmt.Println("🔍 [调试步骤24] 开始PING命令测试")
		testResults = append(testResults, "\n🔍 步骤5: PING命令测试")
		fmt.Println("🔍 [调试步骤25] 发送PING命令")
		_, err = conn.Write([]byte("PING\r\n"))
		if err != nil {
			fmt.Printf("❌ [调试步骤26] 发送PING命令失败: %v\n", err)
			fyne.Do(func() {
				dialog.ShowError(fmt.Errorf("❌ 发送PING命令失败: %v", err), l.window)
			})
			return
		}
		
		// 读取PING响应
		buffer = make([]byte, 1024)
		fmt.Println("🔍 [调试步骤27] 等待PING响应...")
		n, err = conn.Read(buffer)
		if err != nil {
			fmt.Printf("❌ [调试步骤28] 读取PING响应失败: %v\n", err)
			fyne.Do(func() {
				dialog.ShowError(fmt.Errorf("❌ 读取PING响应失败: %v", err), l.window)
			})
			return
		}
		
		response = strings.TrimSpace(string(buffer[:n]))
		fmt.Printf("🔍 [调试步骤29] 收到PING响应: '%s'\n", response)
		if strings.HasPrefix(response, "+PONG") {
			fmt.Println("✅ [调试步骤30] PING测试成功，Redis响应正常")
			testResults = append(testResults, "✅ PING测试成功，Redis响应正常")
		} else {
			fmt.Printf("❌ [调试步骤31] PING测试失败，期望+PONG，实际: %s\n", response)
			fyne.Do(func() {
				dialog.ShowError(fmt.Errorf("❌ PING测试失败\n\n期望响应: +PONG\n实际响应: %s", response), l.window)
			})
			return
		}
		
		// 6. 基本读写测试
		fmt.Println("🔍 [调试步骤32] 开始基本读写功能测试")
		testResults = append(testResults, "\n🔍 步骤6: 基本读写功能测试")
		
		// 设置一个测试键值
		testKey := "gva_launcher_test"
		testValue := fmt.Sprintf("test_%d", time.Now().Unix())
		setCmd := fmt.Sprintf("SET %s %s\r\n", testKey, testValue)
		
		fmt.Printf("🔍 [调试步骤33] 发送SET命令: %s = %s\n", testKey, testValue)
		_, err = conn.Write([]byte(setCmd))
		if err != nil {
			fmt.Printf("❌ [调试步骤34] 发送SET命令失败: %v\n", err)
			fyne.Do(func() {
				dialog.ShowError(fmt.Errorf("❌ 发送SET命令失败: %v", err), l.window)
			})
			return
		}
		
		// 读取SET响应
		buffer = make([]byte, 1024)
		fmt.Println("🔍 [调试步骤35] 等待SET响应...")
		n, err = conn.Read(buffer)
		if err != nil {
			fmt.Printf("❌ [调试步骤36] 读取SET响应失败: %v\n", err)
			fyne.Do(func() {
				dialog.ShowError(fmt.Errorf("❌ 读取SET响应失败: %v", err), l.window)
			})
			return
		}
		
		response = strings.TrimSpace(string(buffer[:n]))
		fmt.Printf("🔍 [调试步骤37] 收到SET响应: '%s'\n", response)
		if !strings.HasPrefix(response, "+OK") {
			fmt.Printf("❌ [调试步骤38] SET命令失败: %s\n", response)
			fyne.Do(func() {
				dialog.ShowError(fmt.Errorf("❌ SET命令失败\n\n响应: %s", response), l.window)
			})
			return
		}
		fmt.Println("✅ [调试步骤39] SET命令执行成功")
		
		// 读取测试键值
		getCmd := fmt.Sprintf("GET %s\r\n", testKey)
		fmt.Printf("🔍 [调试步骤40] 发送GET命令: %s\n", testKey)
		_, err = conn.Write([]byte(getCmd))
		if err != nil {
			fmt.Printf("❌ [调试步骤41] 发送GET命令失败: %v\n", err)
			fyne.Do(func() {
				dialog.ShowError(fmt.Errorf("❌ 发送GET命令失败: %v", err), l.window)
			})
			return
		}
		
		// 读取GET响应
		buffer = make([]byte, 1024)
		fmt.Println("🔍 [调试步骤42] 等待GET响应...")
		n, err = conn.Read(buffer)
		if err != nil {
			fmt.Printf("❌ [调试步骤43] 读取GET响应失败: %v\n", err)
			fyne.Do(func() {
				dialog.ShowError(fmt.Errorf("❌ 读取GET响应失败: %v", err), l.window)
			})
			return
		}
		
		response = strings.TrimSpace(string(buffer[:n]))
		fmt.Printf("🔍 [调试步骤44] 收到GET响应: '%s'\n", response)
		if strings.Contains(response, testValue) {
			fmt.Println("✅ [调试步骤45] 读写功能测试成功")
			testResults = append(testResults, "✅ 读写功能测试成功")
		} else {
			fmt.Printf("❌ [调试步骤46] 读写功能测试失败，期望: %s，实际: %s\n", testValue, response)
			fyne.Do(func() {
				dialog.ShowError(fmt.Errorf("❌ 读写功能测试失败\n\n期望值: %s\n实际响应: %s", testValue, response), l.window)
			})
			return
		}
		
		// 清理测试数据
		delCmd := fmt.Sprintf("DEL %s\r\n", testKey)
		fmt.Printf("🔍 [调试步骤47] 清理测试数据: %s\n", testKey)
		conn.Write([]byte(delCmd))
		
		// 所有测试通过，显示详细结果
		fmt.Println("🎉 [调试步骤48] 所有测试通过！Redis配置完全正确")
		testResults = append(testResults, "\n🎉 所有测试通过！Redis配置完全正确。")
		
		resultMsg := strings.Join(testResults, "\n")
		
		var summaryMsg string
		if password != "" {
			summaryMsg = fmt.Sprintf("✅ Redis连接测试完成！\n\n📋 测试详情:\n%s\n\n📊 配置摘要:\n• 地址: %s\n• 认证: ✓ 密码验证通过\n• 数据库: %d\n• 功能: ✓ 读写正常\n\n🚀 配置无误，可以安全使用！", resultMsg, addr, db)
		} else {
			summaryMsg = fmt.Sprintf("✅ Redis连接测试完成！\n\n📋 测试详情:\n%s\n\n📊 配置摘要:\n• 地址: %s\n• 认证: 无密码模式\n• 数据库: %d\n• 功能: ✓ 读写正常\n\n🚀 配置无误，可以安全使用！", resultMsg, addr, db)
		}
		
		// 先隐藏进度对话框，再显示成功对话框
		fyne.Do(func() {
			progress.Hide()
			dialog.ShowInformation("测试成功", summaryMsg, l.window)
		})
	}()
}

// startStatusMonitor 启动状态监控（定期检查服务实际运行状态）
func (l *GVALauncher) startStatusMonitor() {
	// 开始监控服务状态
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	
	// 监控 30 秒（启动期间）
	timeout := time.After(30 * time.Second)
	checkCount := 0
	
	for {
		select {
		case <-ticker.C:
			checkCount++
			
			// 如果状态监控被暂停，跳过本次检查
			if l.pauseStatusMonitor {
				continue
			}
			
			// 检查端口占用情况
			backendRunning := l.isPortInUse(l.backendPort)
			frontendRunning := l.isPortInUse(l.frontendPort)
			
			// 监控服务状态
			
			// 更新内部状态
			l.backendService.IsRunning = backendRunning
			l.frontendService.IsRunning = frontendRunning
			
			// 更新 UI 显示
			l.updateServiceStatus()
			
			// 如果两个服务都已启动，可以减少监控频率
			if backendRunning && frontendRunning {
				// 两个服务都已启动
				ticker.Reset(5 * time.Second) // 改为每 5 秒检查一次
			}
			
		case <-timeout:
			// 30 秒后改为每 5 秒检查一次
			// 30秒监控期结束
			ticker.Reset(5 * time.Second)
			return
		}
	}
}

// ========================================
// 缓存清理功能
// ========================================

// getGoModCache 获取 Go 模块缓存目录
func (l *GVALauncher) getGoModCache() (string, error) {
	cmd := createHiddenCmd("go", "env", "GOMODCACHE")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("获取 Go 缓存目录失败: %v", err)
	}
	
	return strings.TrimSpace(string(output)), nil
}

// cleanAllCache 清理所有缓存（主函数）
func (l *GVALauncher) cleanAllCache() {
	if l.config.GVARootPath == "" {
		dialog.ShowError(fmt.Errorf("请先指定 GVA 根目录"), l.window)
		return
	}
	
	// 显示确认对话框
	dialog.ShowConfirm(
		"⚠️ 清理缓存确认",
		"此操作将清理 GVA 前后端所有缓存文件:\n\n"+
			"• 前端: web/node_modules/\n"+
			"• 后端: Go 模块缓存 (保留 go.sum)\n\n"+
			"清理后需要重新安装依赖才能运行。\n\n"+
			"是否继续？",
		func(confirmed bool) {
			if !confirmed {
				return
			}
			
			// 用户确认，开始清理
			l.performCacheClean()
		},
		l.window,
	)
}

// performCacheClean 执行缓存清理
func (l *GVALauncher) performCacheClean() {
	// 检查服务是否在运行，如果在运行则先停止
	wasRunning := l.backendService.IsRunning || l.frontendService.IsRunning
	
	// 如果服务正在运行，先停止所有服务
	if wasRunning {
		l.stopGVA()
	}
	
	// 显示进度对话框
	progress := dialog.NewProgressInfinite("清理缓存", "正在清理缓存...", l.window)
	progress.Show()
	
	go func() {
		var wg sync.WaitGroup
		var mu sync.Mutex
		var errors []string
		successCount := 0
		failCount := 0
		
		wg.Add(2)
		
		// 任务1: 并发清理前端缓存
		go func() {
			defer wg.Done()
			err := l.cleanFrontendCache()
			
			mu.Lock()
			if err != nil {
				errors = append(errors, "前端: "+err.Error())
				failCount++
			} else {
				successCount++
			}
			mu.Unlock()
		}()
		
		// 任务2: 并发清理后端缓存
		go func() {
			defer wg.Done()
			backendSuccess, backendFail, err := l.cleanBackendCache(func(current, total int, moduleName string) {
				// 进度更新只能通过关闭旧对话框、显示新对话框来实现
				// 这里简化处理，只显示固定消息
			})
			
			mu.Lock()
			successCount += backendSuccess
			failCount += backendFail
			if err != nil {
				errors = append(errors, "后端: "+err.Error())
			}
			mu.Unlock()
		}()
		
		// 等待两个清理任务都完成
		wg.Wait()
		
		fyne.Do(func() {
			progress.Hide()
		})
		
		// 显示结果
		if len(errors) > 0 {
			msg := fmt.Sprintf("清理完成（部分失败）\n\n✅ 成功: %d\n❌ 失败: %d\n\n错误:\n%s",
				successCount, failCount, strings.Join(errors, "\n"))
			dialog.ShowInformation("清理结果", msg, l.window)
		} else {
			var msg string
			if wasRunning {
				msg = fmt.Sprintf("✅ 清理成功！\n\n已清理 %d 项缓存\n\n服务已自动关闭，请重新安装依赖后启动", successCount)
			} else {
				msg = fmt.Sprintf("✅ 清理成功！\n\n已清理 %d 项缓存\n\n提示: 请运行「安装依赖」重新安装", successCount)
			}
			dialog.ShowInformation("清理成功", msg, l.window)
		}
		
		// 更新依赖状态
		l.checkDependencies()
	}()
}

// cleanFrontendCache 清理前端缓存（删除 node_modules）
func (l *GVALauncher) cleanFrontendCache() error {
	nodeModulesPath := filepath.Join(l.config.GVARootPath, "web", "node_modules")
	
	// 检查目录是否存在
	if !l.dirExists(nodeModulesPath) {
		// node_modules目录不存在
		return nil // 目录不存在，无需清理
	}
	
	// 开始删除node_modules
	
	// 删除 node_modules 目录
	err := os.RemoveAll(nodeModulesPath)
	if err != nil {
		// 前端缓存清理失败
		return fmt.Errorf("删除 node_modules 失败: %v", err)
	}
	
	// 前端缓存清理成功
	return nil
}

// cleanBackendCache 清理后端缓存（循环删除 Go 模块）
func (l *GVALauncher) cleanBackendCache(progressCallback func(current, total int, moduleName string)) (successCount, failCount int, err error) {
	// 1. 获取 Go 缓存目录
	modCache, err := l.getGoModCache()
	if err != nil {
		// 获取Go缓存目录失败
		return 0, 0, err
	}
	// Go缓存目录已获取
	
	// 2. 读取后端依赖列表
	serverPath := filepath.Join(l.config.GVARootPath, "server")
	// 读取依赖列表
	cmd := createHiddenCmd("go", "list", "-m", "all")
	cmd.Dir = serverPath
	output, err := cmd.Output()
	if err != nil {
		// 读取依赖列表失败
		return 0, 0, fmt.Errorf("读取依赖列表失败: %v", err)
	}
	
	// 3. 解析依赖列表
	lines := strings.Split(string(output), "\n")
	var modules []string
	
	// 解析依赖列表
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		
		// 跳过主模块（第一行）
		if strings.HasPrefix(line, "github.com/flipped-aurora/gin-vue-admin/server") {
			// 跳过主模块
			continue
		}
		
		// 格式: 模块名 版本号
		// 例如: github.com/gin-gonic/gin v1.9.1
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			moduleName := parts[0]
			version := parts[1]
			// 构建目录名: 模块名@版本号
			moduleDir := moduleName + "@" + version
			modules = append(modules, moduleDir)
			// 找到依赖
		}
	}
	
	// 共找到依赖模块
	
	// 4. 循环删除每个模块
	total := len(modules)
	// 开始删除模块缓存
	for i, moduleDir := range modules {
		// 更新进度
		if progressCallback != nil {
			progressCallback(i+1, total, moduleDir)
		}
		
		// Go 模块缓存路径需要处理大小写转换
		// 例如: github.com/Masterminds/semver/v3@v3.2.0
		// 实际路径: github.com/!masterminds/semver/v3@v3.2.0
		encodedModuleDir := encodeModulePath(moduleDir)
		
		// 构建完整路径
		modulePath := filepath.Join(modCache, encodedModuleDir)
		
		// 删除模块
		// 模块路径已构建
		
		// 删除模块目录
		err := os.RemoveAll(modulePath)
		if err != nil {
			// 删除失败
			failCount++
		} else {
			// 删除成功
			successCount++
		}
	}
	
	// 5. 不删除 go.sum 文件（Go 项目必需文件）
	// 注意：go.sum 文件包含依赖包的校验和，删除会导致启动失败
	// 保留go.sum文件
	
	// 后端缓存清理完成
	return successCount, failCount, nil
}

// encodeModulePath 将模块路径编码为 Go 缓存的实际路径格式
// Go 模块缓存中，大写字母会被转换为 !小写字母
func encodeModulePath(modulePath string) string {
	var result strings.Builder
	for _, r := range modulePath {
		if r >= 'A' && r <= 'Z' {
			result.WriteRune('!')
			result.WriteRune(r + 32) // 转换为小写
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}

