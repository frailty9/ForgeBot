// Package configs 提供程序内嵌的唯一运行时配置示例。
package configs

import _ "embed"

//go:embed runtime.example.json
var runtimeExample string

// RuntimeExample 返回完整示例的副本，供 init 生成部署配置。
func RuntimeExample() []byte { return []byte(runtimeExample) }
