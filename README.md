# GVAPanel

<p align="center">
  <img src="GVAPanel.png" alt="GVAPanel Logo" width="200"/>
</p>

<p align="center">
  <strong>🎛️ GVA 面板 - 为 Gin-Vue-Admin 打造的可视化管理工具</strong>
</p>

<p align="center">
  <a href="#主要功能">功能</a> •
  <a href="#系统要求">要求</a> •
  <a href="#安装使用">安装</a> •
  <a href="#使用说明">使用</a> •
  <a href="#编译构建">构建</a> •
  <a href="#开源协议">协议</a>
</p>

---

## 📖 简介

**GVAPanel** 是一个专为 [Gin-Vue-Admin](https://www.gin-vue-admin.com/) 项目设计的图形化管理面板，提供可视化的项目管理、服务控制、配置优化等功能，让 GVA 项目的开发和部署变得更加简单高效。

### ✨ 主要功能

- 🚀 **服务管理** - 一键启动/停止前后端服务，实时监控运行状态
- 📦 **依赖管理** - 自动检测和安装前后端依赖，支持批量操作
- 🔧 **配置管理** - 可视化编辑端口、Redis、数据库等配置
- 🌐 **镜像源切换** - 快速切换 npm 和 GOPROXY 镜像源
- 🧹 **缓存清理** - 智能清理前后端缓存，释放磁盘空间
- 📊 **状态监控** - 实时显示服务运行状态和端口占用情况
- 🔗 **快速访问** - 一键复制访问链接，支持局域网 IP 自动识别
- 🎨 **美观界面** - 现代化 UI 设计，操作简洁流畅

---

## 🖥️ 系统要求

- **操作系统**: Windows 10/11 (64-bit)
- **Go**: 1.18 或更高版本
- **Node.js**: 16.x 或更高版本
- **包管理器**: npm 或 yarn
- **Redis**: 可选（如果 GVA 项目使用 Redis）

---

## 📦 安装使用

### 方式一：直接下载可执行文件（推荐）

1. 前往 [Releases](https://github.com/小阿凤俱乐部/GVAPanel/releases) 页面
2. 下载最新版本的 `GVAPanel_for_windows.exe`
3. 双击运行，首次启动会要求选择 GVA 项目根目录
4. 选择您的 GVA 项目目录后即可开始使用

### 方式二：从源码编译

详见 [编译构建](#编译构建) 章节

---

## 🎯 使用说明

### 首次使用

1. **启动 GVAPanel**
   - 双击 `GVAPanel_for_windows.exe` 运行

2. **选择 GVA 根目录**
   - 点击"浏览"按钮，选择您的 GVA 项目根目录
   - 项目结构应包含 `server/` 和 `web/` 目录

3. **检查依赖**
   - GVAPanel 会自动检测前后端依赖安装状态
   - 如需安装依赖，点击"安装依赖"按钮

4. **启动服务**
   - 点击"🚀 启动服务"按钮
   - 等待服务启动完成（通常需要 10-30 秒）
   - 启动成功后可点击"打开前端"或"复制链接"

### 功能介绍

#### 📂 目录管理
- **选择目录**: 浏览并选择 GVA 项目根目录
- **自动配置**: 自动读取项目配置文件

#### 🔧 配置管理
- **端口配置**: 修改前后端服务端口
- **Redis 配置**: 配置 Redis 连接信息并测试连接
- **镜像源配置**: 
  - 前端：切换 npm registry（支持淘宝、腾讯云等镜像）
  - 后端：切换 GOPROXY（支持七牛云、阿里云等镜像）

#### 📦 依赖管理
- **依赖检测**: 自动检测前后端依赖安装状态
- **安装依赖**: 
  - 前端：执行 `npm install`
  - 后端：执行 `go mod download`
- **缓存清理**: 清理 npm 缓存和 Go 模块缓存

#### 🚀 服务控制
- **启动服务**: 同时启动前后端服务
- **停止服务**: 安全停止所有服务进程
- **状态监控**: 实时显示服务运行状态
- **快速访问**: 
  - 点击"打开前端"在浏览器中访问
  - 点击"复制链接"复制访问地址（支持局域网 IP）

---

## 🛠️ 编译构建

### 前置要求

1. **安装 Go 1.18+**
   ```bash
   go version
   ```

2. **安装 Fyne 依赖**（自动通过 go.mod 管理）

3. **安装 go-winres**（用于嵌入图标）
   ```bash
   go install github.com/tc-hib/go-winres@latest
   ```

### 克隆项目

```bash
git clone https://github.com/小阿凤俱乐部/GVAPanel.git
cd GVAPanel
```

### 编译步骤

#### 1. 生成 Windows 资源文件（图标）

```bash
go-winres simply --icon GVAPanel.png --product-name GVAPanel --file-description "GVA Panel - Visual Management Tool" --product-version 1.0.0 --file-version 1.0.0
```

#### 2. 编译可执行文件

```bash
# 编译为 Windows GUI 程序（无控制台窗口）
go build -ldflags "-H windowsgui" -o GVAPanel_for_windows.exe main.go
```

#### 3. 运行程序

```bash
# 直接运行
.\GVAPanel_for_windows.exe
```

### 编译选项说明

- `-ldflags "-H windowsgui"`: 编译为 Windows GUI 程序，不显示控制台窗口
- 如需调试，可以去掉此参数：
  ```bash
  go build -o GVAPanel_for_windows.exe main.go
  ```

---

## 📸 功能截图

<details>
<summary>点击展开查看截图</summary>

<!-- 在这里添加软件截图 -->
（待添加）
<img width="1078" height="1470" alt="image" src="https://github.com/user-attachments/assets/8fa5c810-1a32-4586-923d-8c43c5a8dad8" />

</details>

---

## 🧰 技术栈

- **GUI 框架**: [Fyne v2.5+](https://fyne.io/) - 跨平台 Go GUI 框架
- **编程语言**: Go 1.21+
- **图标工具**: [go-winres](https://github.com/tc-hib/go-winres) - Windows 资源嵌入工具
- **构建工具**: Go Modules

---

## 📂 项目结构

```
GVAPanel/
├── main.go                 # 主程序代码
├── go.mod                  # Go 模块依赖
├── go.sum                  # 依赖锁定文件
├── GVAPanel.png           # 应用程序图标
├── README.md              # 项目说明文档
├── LICENSE                # 开源协议
└── .gitignore            # Git 忽略文件
```

---

## 🤝 贡献指南

欢迎贡献代码、提出建议或报告问题！

### 如何贡献

1. Fork 本仓库
2. 创建您的特性分支 (`git checkout -b feature/AmazingFeature`)
3. 提交您的更改 (`git commit -m 'Add some AmazingFeature'`)
4. 推送到分支 (`git push origin feature/AmazingFeature`)
5. 开启一个 Pull Request

### 报告问题

如果您发现 bug 或有功能建议，请 [提交 Issue](https://github.com/小阿凤俱乐部/GVAPanel/issues/new)

---

## 📋 更新日志

### v1.0.0 (2025-01-XX)

#### ✨ 新功能
- 🎛️ 图形化管理界面
- 🚀 前后端服务一键启停
- 📦 依赖自动检测与安装
- 🔧 可视化配置管理
- 🌐 镜像源快速切换
- 🧹 智能缓存清理
- 📊 实时状态监控
- 🔗 快速访问链接复制

---

## ❓ 常见问题

<details>
<summary><b>Q: 启动服务失败怎么办？</b></summary>

A: 请检查：
1. GVA 目录是否正确
2. 端口是否被占用
3. 依赖是否完整安装
4. Redis 配置是否正确（如使用）
</details>

<details>
<summary><b>Q: 依赖检测显示未安装，但实际已安装？</b></summary>

A: 可能是缓存问题，尝试：
1. 点击"清理缓存"
2. 重新"安装依赖"
3. 重启 GVAPanel
</details>

<details>
<summary><b>Q: 支持 macOS 或 Linux 吗？</b></summary>

A: 目前仅支持 Windows，后续版本会考虑支持其他平台。
</details>

---

## 📄 开源协议

本项目采用 [MIT License](LICENSE) 开源协议

```
MIT License

Copyright (c) 2025 GVAPanel Contributors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction...
```

---

## ⭐ Star History

如果 GVAPanel 对您有帮助，请给个 Star ⭐ 支持一下！

[![Star History Chart](https://api.star-history.com/svg?repos=小阿凤俱乐部/GVAPanel&type=Date)](https://star-history.com/#小阿凤俱乐部/GVAPanel&Date)

---

## 📧 联系方式

- **Issues**: [GitHub Issues](https://github.com/小阿凤俱乐部/GVAPanel/issues)
- **Discussions**: [GitHub Discussions](https://github.com/小阿凤俱乐部/GVAPanel/discussions)

---

## 🙏 致谢

- [Gin-Vue-Admin](https://www.gin-vue-admin.com/) - 优秀的前后端分离框架
- [Fyne](https://fyne.io/) - 强大的 Go GUI 框架
- 所有贡献者和使用者

---

<p align="center">
  Made with ❤️ by GVAPanel Contributors
</p>


