package filestore

import "golang.org/x/sys/unix"

func usageOf(path string) (Usage, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return Usage{}, err
	}
	free := stat.Bavail * uint64(stat.Bsize)
	total := stat.Blocks * uint64(stat.Bsize)

	usage := Usage{TotalBytes: total, FreeBytes: free}
	if total > 0 {
		usage.UsedPercentage = 100 * float64(total-free) / float64(total)
	}
	return usage, nil
}
