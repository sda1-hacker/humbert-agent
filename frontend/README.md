# Humbert Agent 前端

Vue 3 + Vite 前端，桌面服务由 Wails 3 提供。项目总览与数据恢复说明见仓库根目录 README。

```bash
npm ci
npm test
npm run build
```

`npm run build` 会重建 `dist/.gitkeep`，保证 Go 的 `go:embed` 在首次构建前和构建后均能读取该目录。
