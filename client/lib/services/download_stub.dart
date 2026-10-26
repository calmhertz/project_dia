// Platform stub for saving a downloaded file; web replaces this.

void saveDownloadedBytes(String fileName, List<int> bytes) =>
    throw UnsupportedError('saving files is implemented for web builds only');
