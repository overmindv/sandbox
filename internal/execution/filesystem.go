package execution

func TotalFileBytes(files []ExecutionFile) int {
	total := 0
	for _, file := range files {
		total += len(file.Content)
	}
	return total
}
