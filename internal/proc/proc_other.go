//go:build !darwin && !linux && !windows

package proc

// localOwners 在未适配的平台上不提供进程信息，进程列显示为空。
func localOwners() (map[uint16]Owner, error) { return nil, nil }
