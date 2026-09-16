package main

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"syscall"
)

// printTree renders the directory tree and per-user/group summaries.
func printTree(rootAbs string, children map[string][]string, dirStats map[string]*Stat, userStats map[string]*Stat,
	groupStats map[string]*Stat, sizeStrMap, userSizeStr, groupSizeStr map[string]string, maxSizeWidth,
	maxFilesWidth int, levels int, showFiles, showUser, showGroup, bytesFlag bool, topN int, readMode bool,
	readOwners, readGroups map[string]string) {
	// copy dirSizes from dirStats
	dirSizes := make(map[string]int64, len(dirStats))
	for k, v := range dirStats {
		dirSizes[k] = v.Size
	}

	// sort children lists by descending total size (fallback to name)
	for k := range children {
		s := children[k]
		sort.Slice(s, func(i, j int) bool {
			si := dirSizes[s[i]]
			sj := dirSizes[s[j]]
			if si == sj {
				return s[i] < s[j]
			}
			return si > sj
		})
		children[k] = s
	}

	// printing header
	var headerCols []any
	headerFmt := fmt.Sprintf("%%%ds", maxSizeWidth)
	headerCols = append(headerCols, "Size")
	if showFiles {
		headerFmt += " %" + strconv.Itoa(maxFilesWidth) + "s"
		headerCols = append(headerCols, "Files")
	}
	if showUser {
		headerFmt += " %-15s"
		headerCols = append(headerCols, "User")
	}
	if showGroup {
		headerFmt += " %-15s"
		headerCols = append(headerCols, "Group")
	}
	headerFmt += " %s\n"
	headerCols = append(headerCols, "Path")
	fmt.Printf(headerFmt, headerCols...)

	var printDirRec func(pathRel string, curLevel int, prefix string, isLast bool)
	printDirRec = func(pathRel string, curLevel int, prefix string, isLast bool) {
		stat := dirStats[pathRel]
		// size string
		sizeCombined := "0"
		if val, ok := sizeStrMap[pathRel]; ok {
			sizeCombined = val
		} else if stat != nil {
			if bytesFlag {
				sizeCombined = strconv.FormatInt(stat.Size, 10)
			} else {
				sizeCombined = humanizeBytes(stat.Size)
			}
		}
		filesStr := ""
		if showFiles {
			if stat != nil {
				filesStr = strconv.FormatInt(stat.Files, 10)
			} else {
				filesStr = "0"
			}
		}

		userStr := ""
		groupStr := ""
		if showUser || showGroup {
			if readMode {
				if showUser {
					if v, ok := readOwners[pathRel]; ok {
						userStr = v
					}
				}
				if showGroup {
					if v, ok := readGroups[pathRel]; ok {
						groupStr = v
					}
				}
			} else {
				full := rootAbs
				if pathRel != "." {
					full = filepath.Join(rootAbs, pathRel)
				}
				if info, err := os.Lstat(full); err == nil {
					if st, ok := info.Sys().(*syscall.Stat_t); ok {
						uidStr := strconv.FormatUint(uint64(st.Uid), 10)
						gidStr := strconv.FormatUint(uint64(st.Gid), 10)
						if showUser {
							if u, err := user.LookupId(uidStr); err == nil {
								userStr = u.Username
							} else {
								userStr = uidStr
							}
						}
						if showGroup {
							if g, err := user.LookupGroupId(gidStr); err == nil {
								groupStr = g.Name
							} else {
								groupStr = gidStr
							}
						}
					}
				}
			}
		}

		var name string
		if curLevel == 0 {
			name = rootAbs
		} else {
			connector := ""
			if isLast {
				connector = "└── "
			} else {
				connector = "├── "
			}
			name = prefix + connector + filepath.Base(pathRel)
		}

		fmtStr := fmt.Sprintf("%%%ds", maxSizeWidth)
		args := []any{sizeCombined}
		if showFiles {
			fmtStr += " %" + strconv.Itoa(maxFilesWidth) + "s"
			args = append(args, filesStr)
		}
		if showUser {
			fmtStr += " %-15s"
			args = append(args, userStr)
		}
		if showGroup {
			fmtStr += " %-15s"
			args = append(args, groupStr)
		}
		fmtStr += " %s\n"
		args = append(args, name)
		fmt.Printf(fmtStr, args...)

		if curLevel >= levels {
			return
		}

		kids := children[pathRel]
		for i, k := range kids {
			last := i == len(kids)-1
			childPrefix := prefix
			if curLevel >= 0 {
				if isLast {
					childPrefix += "    "
				} else {
					childPrefix += "│   "
				}
			}
			printDirRec(k, curLevel+1, childPrefix, last)
		}
	}

	if _, ok := dirStats["."]; !ok {
		dirStats["."] = &Stat{}
	}

	printDirRec(".", 0, "", true)

	// per-user summary
	fmt.Println()
	fmt.Println("Per-user summary:")
	userNames := make([]string, 0, len(userStats))
	for u := range userStats {
		userNames = append(userNames, u)
	}
	sort.Slice(userNames, func(i, j int) bool { return userStats[userNames[i]].Size > userStats[userNames[j]].Size })
	if topN > 0 && topN < len(userNames) {
		userNames = userNames[:topN]
	}
	for _, u := range userNames {
		s := userStats[u]
		// combined user size string
		sizeCombined := "0"
		if val, ok := userSizeStr[u]; ok {
			sizeCombined = val
		} else if s != nil {
			if bytesFlag {
				sizeCombined = strconv.FormatInt(s.Size, 10)
			} else {
				sizeCombined = humanizeBytes(s.Size)
			}
		}
		filesCount := int64(0)
		if s != nil {
			filesCount = s.Files
		}
		fmt.Printf("%-20s %"+strconv.Itoa(maxSizeWidth)+"s %"+strconv.Itoa(maxFilesWidth)+"d files\n", u, sizeCombined, filesCount)
	}

	// per-group summary
	fmt.Println()
	fmt.Println("Per-group summary:")
	groupNames := make([]string, 0, len(groupStats))
	for g := range groupStats {
		groupNames = append(groupNames, g)
	}
	sort.Slice(groupNames, func(i, j int) bool { return groupStats[groupNames[i]].Size > groupStats[groupNames[j]].Size })
	if topN > 0 && topN < len(groupNames) {
		groupNames = groupNames[:topN]
	}
	for _, g := range groupNames {
		s := groupStats[g]
		sizeCombined := "0"
		if val, ok := groupSizeStr[g]; ok {
			sizeCombined = val
		} else if s != nil {
			if bytesFlag {
				sizeCombined = strconv.FormatInt(s.Size, 10)
			} else {
				sizeCombined = humanizeBytes(s.Size)
			}
		}
		filesCount := int64(0)
		if s != nil {
			filesCount = s.Files
		}
		fmt.Printf("%-20s %"+strconv.Itoa(maxSizeWidth)+"s %"+strconv.Itoa(maxFilesWidth)+"d files\n", g, sizeCombined, filesCount)
	}
}
