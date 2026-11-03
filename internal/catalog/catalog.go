// Package catalog 定义对部署者展示的能力投影，不取代各子系统状态权威源。
package catalog

type Entry struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Source        string `json:"source"`
	Enabled       bool   `json:"enabled"`
	Configured    bool   `json:"configured"`
	Loaded        bool   `json:"loaded"`
	Health        string `json:"health"`
	LastError     string `json:"last_error,omitempty"`
	Permission    string `json:"permission,omitempty"`
	Documentation string `json:"documentation,omitempty"`
}
type Snapshot struct {
	Version uint64  `json:"version"`
	Entries []Entry `json:"entries"`
}
