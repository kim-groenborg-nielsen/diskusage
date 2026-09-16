package main

import "strconv"

// ComputeSizeMapsAndWidths builds combined size strings (mantissa+unit or raw bytes)
// for directories, users, and groups and returns maps plus auto-fit widths for
// the size column and files column.
func ComputeSizeMapsAndWidths(dirSizes map[string]int64, dirStats, userStats, groupStats StatMap, bytesFlag bool,
	sizeWidthOverride, filesWidthOverride int) (map[string]string, map[string]string, map[string]string, int, int) {
	sizeStrMap := make(map[string]string, len(dirSizes))
	maxSizeWidth := 0
	maxFilesWidth := 0
	for p, s := range dirSizes {
		var combined string
		if bytesFlag {
			combined = strconv.FormatInt(s, 10)
		} else {
			combined = humanizeBytes(s)
		}
		sizeStrMap[p] = combined
		if w := len(combined); w > maxSizeWidth {
			maxSizeWidth = w
		}
		if st, ok := dirStats[p]; ok {
			fsStr := strconv.FormatInt(st.Files, 10)
			if w := len(fsStr); w > maxFilesWidth {
				maxFilesWidth = w
			}
		}
	}

	userSizeStr := statMapToStringMap(userStats, bytesFlag, &maxSizeWidth, &maxFilesWidth)
	groupSizeStr := statMapToStringMap(groupStats, bytesFlag, &maxSizeWidth, &maxFilesWidth)

	// apply overrides if provided
	if sizeWidthOverride > 0 {
		maxSizeWidth = sizeWidthOverride
	}
	if filesWidthOverride > 0 {
		maxFilesWidth = filesWidthOverride
	}

	if maxSizeWidth < 4 {
		maxSizeWidth = 4
	}
	if maxFilesWidth < 3 {
		maxFilesWidth = 3
	}

	return sizeStrMap, userSizeStr, groupSizeStr, maxSizeWidth, maxFilesWidth
}

func statMapToStringMap(stats StatMap, bytesFlag bool, maxSizeWidth, maxFilesWidth *int) map[string]string {
	result := make(map[string]string, len(stats))
	for k, v := range stats {
		if bytesFlag {
			result[k] = strconv.FormatInt(v.Size, 10)
		} else {
			result[k] = humanizeBytes(v.Size)
		}
		if len(result[k]) > *maxSizeWidth {
			*maxSizeWidth = len(result[k])
		}
		fsStr := strconv.FormatInt(v.Files, 10)
		if len(fsStr) > *maxFilesWidth {
			*maxFilesWidth = len(fsStr)
		}
	}
	return result
}
