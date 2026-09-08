package filestore

import "golang.org/x/sys/unix"

// usageOf reads the filesystem holding path.
func usageOf(path string) (Usage, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return Usage{}, err
	}
	// Bavail, not Bfree: blocks reserved for root are not ours to use.
	free := stat.Bavail * uint64(stat.Bsize)
	total := stat.Blocks * uint64(stat.Bsize)

	usage := Usage{TotalBytes: total, FreeBytes: free}
	if total > 0 {
		usage.UsedPercentage = 100 * float64(total-free) / float64(total)
	}
	return usage, nil
}
