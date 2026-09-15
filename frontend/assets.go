package frontend

// 这是一个桥接包，用于声明frontend这个包和资产文件

import "embed"

//go:embed all:dist
var Assets embed.FS
