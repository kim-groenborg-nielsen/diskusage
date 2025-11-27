package main

import "strconv"

// ComputeSizeMapsAndWidths builds combined size strings (mantissa+unit or raw bytes)
// for directories, users, and groups and returns maps plus auto-fit widths for
// the size column and files column.
func ComputeSizeMapsAndWidths(dirSizes map[string]int64, dirStats map[string]*DirStat, userStats map[string]*UserStat, groupStats map[string]*GroupStat, bytesFlag bool, sizeWidthOverride, filesWidthOverride int) (map[string]string, map[string]string, map[string]string, int, int) {
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

	userSizeStr := make(map[string]string, len(userStats))
	for u, us := range userStats {
		if bytesFlag {
			userSizeStr[u] = strconv.FormatInt(us.Size, 10)
		} else {
			userSizeStr[u] = humanizeBytes(us.Size)
		}
		if len(userSizeStr[u]) > maxSizeWidth {
			maxSizeWidth = len(userSizeStr[u])
		}
		fsStr := strconv.FormatInt(us.Files, 10)
		if len(fsStr) > maxFilesWidth {
			maxFilesWidth = len(fsStr)
		}
	}

	groupSizeStr := make(map[string]string, len(groupStats))
	for g, gs := range groupStats {
		if bytesFlag {
			groupSizeStr[g] = strconv.FormatInt(gs.Size, 10)
		} else {
			groupSizeStr[g] = humanizeBytes(gs.Size)
		}
		if len(groupSizeStr[g]) > maxSizeWidth {
			maxSizeWidth = len(groupSizeStr[g])
		}
		fsStr := strconv.FormatInt(gs.Files, 10)
		if len(fsStr) > maxFilesWidth {
			maxFilesWidth = len(fsStr)
		}
	}

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
