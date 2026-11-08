// Package persistence 选择进程唯一的持久化实现。业务只依赖领域仓储，
// 不接触文件路径、SQL 事务或 NoSQL collection；后端故障不触发第二后端。
package persistence

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/soleapi/forgebot/internal/config"
	"github.com/soleapi/forgebot/internal/operation"
	"github.com/soleapi/forgebot/internal/secret"
	"github.com/soleapi/forgebot/internal/settings"
)

// Backend 聚合同一实现的领域仓储。未配置的仓储返回 nil，调用方必须明确
// 报告 not_configured/unavailable，禁止偷偷创建内存或另一种持久化实现。
// 新的持久业务（如群公告幂等）以独立领域接口加入，不增加通用 Query/Exec 方法。
type Backend interface {
	Kind() string
	Configuration() settings.Store
	Credentials() secret.Store
}

type fileBackend struct {
	configuration settings.Store
	credentials   secret.Store
}

func (*fileBackend) Kind() string                    { return config.FileBackend }
func (b *fileBackend) Configuration() settings.Store { return b.configuration }
func (b *fileBackend) Credentials() secret.Store     { return b.credentials }

// Open 是唯一生产选择入口；未来 SQL/NoSQL 在此加入同级实现。
// key 由进程入口解析，不让仓储自行读取环境变量或接触管理 API。
func Open(static config.StaticConfig, key []byte) (Backend, error) {
	if static.Storage.Kind() != config.FileBackend {
		return nil, operation.New(operation.Unsupported, fmt.Sprintf("unsupported storage backend %q", static.Storage.Kind()))
	}
	if static.RuntimeConfigPath != "" && static.SecretStorePath != "" {
		configurationPath, err := filepath.Abs(static.RuntimeConfigPath)
		if err != nil {
			return nil, err
		}
		credentialPath, err := filepath.Abs(static.SecretStorePath)
		if err != nil {
			return nil, err
		}
		if strings.EqualFold(configurationPath, credentialPath) {
			return nil, operation.New(operation.Invalid, "configuration and credentials must use distinct files")
		}
	}
	b := &fileBackend{}
	if static.RuntimeConfigPath != "" {
		store, err := settings.NewJSONFileStore(static.RuntimeConfigPath, static)
		if err != nil {
			return nil, operation.Wrap("storage.configuration.open", operation.Unavailable, operation.NotStarted, err)
		}
		b.configuration = store
	}
	if static.SecretStorePath != "" {
		if len(key) == 0 {
			return nil, operation.New(operation.NotConfigured, "secret_key (YAML or FORGEBOT_SECRET_KEY) is required when secret_store_path is set")
		}
		store, err := secret.NewFileStore(static.SecretStorePath, key)
		if err != nil {
			return nil, operation.Wrap("storage.credentials.open", operation.Unavailable, operation.NotStarted, err)
		}
		b.credentials = store
	} else if len(key) > 0 {
		return nil, operation.New(operation.NotConfigured, "secret_store_path is required when secret_key is set")
	}
	return b, nil
}
