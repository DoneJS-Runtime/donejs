package vm

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	_ "time/tzdata" // embed the IANA zone database so zones work without /usr/share/zoneinfo
)

// Time zones.  ICU accepts the IANA identifiers (case insensitively), the UTC
// aliases and fixed offsets such as "+05:30"; Go's time package covers the
// IANA half, the rest is handled here.

var zoneCache = map[string]*time.Location{}

// Names ICU treats as UTC, in any case.
var utcNames = map[string]bool{
	"UTC": true, "UCT": true, "UNIVERSAL": true, "ZULU": true,
	"GREENWICH": true, "GMT": true,
}

// The Etc/ spellings of the UTC aliases.
var etcUtcNames = map[string]bool{
	"UTC": true, "UCT": true, "UNIVERSAL": true, "ZULU": true,
	"GREENWICH": true, "GMT": true, "GMT0": true, "GMT+0": true, "GMT-0": true,
}

// intlLocalZone is the host's zone, the default Intl time zone.
func intlLocalZone() string {
	if tz := os.Getenv("TZ"); tz != "" {
		name := strings.TrimPrefix(tz, ":")
		if loadZone(name) != nil {
			return name
		}
	}
	// /etc/localtime usually is a symlink into the zoneinfo tree.
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		for _, root := range []string{"/usr/share/zoneinfo/", "/usr/lib/zoneinfo/",
			"/usr/share/lib/zoneinfo/"} {
			if i := strings.Index(target, root); i >= 0 {
				name := target[i+len(root):]
				if loadZone(name) != nil {
					return name
				}
			}
		}
	}
	if data, err := os.ReadFile("/etc/timezone"); err == nil {
		name := strings.TrimSpace(string(data))
		if loadZone(name) != nil {
			return name
		}
	}
	return "UTC"
}

// parseZoneOffset parses "+05", "+0530" and "-08:00" into seconds east of UTC.
func parseZoneOffset(s string) (int, bool) {
	if len(s) < 3 || len(s) > 6 || (s[0] != '+' && s[0] != '-') {
		return 0, false
	}
	sign := 1
	if s[0] == '-' {
		sign = -1
	}
	digits := s[1:]
	hours, err := strconv.Atoi(digits[0:2])
	if err != nil {
		return 0, false
	}
	minutes := 0
	rest := digits[2:]
	switch {
	case rest == "":
	case len(rest) == 3 && rest[0] == ':':
		minutes, err = strconv.Atoi(rest[1:])
		if err != nil {
			return 0, false
		}
	case len(rest) == 2:
		minutes, err = strconv.Atoi(rest)
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	if hours > 23 || minutes > 59 {
		return 0, false
	}
	return sign * (hours*3600 + minutes*60), true
}

func formatZoneOffset(seconds int) string {
	sign := "+"
	if seconds < 0 {
		sign = "-"
		seconds = -seconds
	}
	return fmt.Sprintf("%s%02d:%02d", sign, seconds/3600, (seconds/60)%60)
}

// loadZone resolves an IANA zone name, caching the result.
func loadZone(id string) *time.Location {
	if id == "" || strings.EqualFold(id, "Local") {
		return nil
	}
	if loc, ok := zoneCache[id]; ok {
		return loc
	}
	loc, err := time.LoadLocation(id)
	if err != nil {
		return nil
	}
	zoneCache[id] = loc
	return loc
}

// titleZone retitles the path segments: "america/new_york" -> "America/New_York".
func titleZone(id string) string {
	parts := strings.Split(id, "/")
	for i, p := range parts {
		if p == "" {
			return id
		}
		parts[i] = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
	}
	return strings.Join(parts, "/")
}

// intlTimeZone returns the canonical name ICU would resolve for id, or "" when
// the identifier is not a time zone.
func intlTimeZone(id string) string {
	if id == "" {
		return ""
	}
	if off, ok := parseZoneOffset(id); ok {
		return formatZoneOffset(off)
	}
	up := strings.ToUpper(id)
	if utcNames[up] {
		return "UTC"
	}
	if up == "GMT0" || up == "GMT+0" || up == "GMT-0" {
		return "UTC"
	}
	if strings.HasPrefix(up, "ETC/") && etcUtcNames[up[4:]] {
		return "UTC"
	}
	if loadZone(id) != nil {
		return id
	}
	if title := titleZone(id); title != id && loadZone(title) != nil {
		return title
	}
	return ""
}

// intlZoneOffset returns the zone's offset in seconds east of UTC at ms.
func intlZoneOffset(id string, ms int64) int {
	if id == "" || id == "UTC" {
		return 0
	}
	if off, ok := parseZoneOffset(id); ok {
		return off
	}
	loc := loadZone(id)
	if loc == nil {
		return 0
	}
	_, off := time.UnixMilli(ms).In(loc).Zone()
	return off
}
