package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/canonical/lxd/lxd/util"
	"github.com/canonical/lxd/shared"
)

// SnapshotScheduleAliases contains the mapping of scheduling aliases to cron syntax
// including placeholders for scheduled time obfuscation.
var SnapshotScheduleAliases = map[string]string{
	"@hourly":   "%s * * * *",
	"@daily":    "%s %s * * *",
	"@midnight": "%s 0 * * *",
	"@weekly":   "%s %s * * 0",
	"@monthly":  "%s %s 1 * *",
	"@annually": "%s %s 1 1 *",
	"@yearly":   "%s %s 1 1 *",
	"@never":    "",
}

func snapshotIsScheduledNow(spec string, subjectID int64) bool {
	var result = false

	now := time.Now()
	specs := buildCronSpecs(spec, subjectID)
	for _, curSpec := range specs {
		isNow, err := shared.CronSpecIsActiveThisMinute(curSpec, now)
		if err == nil && isNow {
			result = true
		}
	}

	return result
}

func buildCronSpecs(spec string, subjectID int64) []string {
	var result []string

	if strings.Contains(spec, ", ") {
		for _, curSpec := range shared.SplitNTrimSpace(spec, ",", -1, true) {
			entry := getCronSyntax(curSpec, subjectID)
			if entry != "" {
				result = append(result, entry)
			}
		}
	} else {
		entry := getCronSyntax(spec, subjectID)
		if entry != "" {
			result = append(result, entry)
		}
	}

	return result
}

func getCronSyntax(spec string, subjectID int64) string {
	alias, isAlias := SnapshotScheduleAliases[strings.ToLower(spec)]
	if isAlias {
		if alias == "@never" {
			return ""
		}

		obfuscatedMinute, obfuscatedHour := getObfuscatedTimeValuesForSubject(subjectID)

		if strings.Count(alias, "%s") > 1 {
			return fmt.Sprintf(alias, obfuscatedMinute, obfuscatedHour)
		}

		return fmt.Sprintf(alias, obfuscatedMinute)
	}

	return spec
}

func getObfuscatedTimeValuesForSubject(subjectID int64) (minuteResult string, hourResult string) {
	minuteResult = "0"
	hourResult = "0"

	minuteSequence, minuteSequenceErr := util.GenerateSequenceInt64(0, 60, 1)
	minute, minuteErr := util.GetStableRandomInt64FromList(subjectID, minuteSequence)
	if minuteErr == nil && minuteSequenceErr == nil {
		minuteResult = strconv.FormatInt(minute, 10)
	}

	hourSequence, hourSequenceErr := util.GenerateSequenceInt64(0, 24, 1)
	hour, hourErr := util.GetStableRandomInt64FromList(subjectID, hourSequence)
	if hourErr == nil && hourSequenceErr == nil {
		hourResult = strconv.FormatInt(hour, 10)
	}

	return minuteResult, hourResult
}
