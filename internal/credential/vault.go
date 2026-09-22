package credential

import (
	"errors"

	keyring "github.com/zalando/go-keyring"
)

// BackupVault 仅暂存用户安排下次启动备份/恢复时输入的口令，不写入数据目录。
type BackupVault struct{}

const backupVaultService = "com.sda1hacker.humbertagent.backup"

func (BackupVault) Set(id, value string) error    { return keyring.Set(backupVaultService, id, value) }
func (BackupVault) Get(id string) (string, error) { return keyring.Get(backupVaultService, id) }
func (BackupVault) Delete(id string) error {
	err := keyring.Delete(backupVaultService, id)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
