package jit

// date.go implements the Date constructor and the instance getters the Intl
// date formatting layer reads. Instances are plain objects of the date class:
// the epoch milliseconds live in the hidden `ms` property and the getters are
// closures over it, so `d.getTime()` and `d.getUTCDate()` behave like real
// methods, `new Date(0) instanceof Date` is true and `Date.prototype` is an
// ordinary object the bootstrap layer can extend.

import (
	"math"
	"strconv"
	"time"
)

const msPerDay = 86400000

var dateClass = newDateClass()

// newDateClass builds the Date constructor class.
func newDateClass() *Class {
	c := &Class{
		Name: "Date",
		Ctor: builtinFn("Date", func([]Value) Value {
			panic(&ThrowError{Value: strErr("TypeError", "Class Date cannot be invoked without 'new'")})
		}),
		Proto:  NewObj(),
		Static: NewObj(),
		Native: initDate,
	}
	c.Static.set("now", builtinFn("Date.now", func([]Value) Value {
		return Num(float64(time.Now().UnixNano()) / 1e6)
	}))
	c.Static.set("parse", builtinFn("Date.parse", func(args []Value) Value {
		if len(args) == 0 {
			return Num(math.NaN())
		}
		return Num(float64(parseDateStr(args[0].toStr())))
	}))
	c.Static.set("UTC", builtinFn("Date.UTC", func(args []Value) Value {
		return Num(float64(dateUTC(args)))
	}))
	return c
}

// initDate is the native constructor body: it stores the epoch milliseconds
// and installs the bound getters.
func initDate(o *Obj, args []Value) *ThrowError {
	var ms int64
	if len(args) > 0 {
		ms = dateArgTime(args[0])
	}
	o.set("ms", Num(float64(ms)))
	o.set("valueOf", builtinFn("valueOf", func([]Value) Value { return Num(float64(ms)) }))
	o.set("getTime", builtinFn("getTime", func([]Value) Value { return Num(float64(ms)) }))
	o.set("toString", builtinFn("toString", func([]Value) Value { return Str(dateString(ms)) }))
	o.set("toISOString", builtinFn("toISOString", func([]Value) Value { return Str(dateISO(ms)) }))
	o.set("toJSON", builtinFn("toJSON", func([]Value) Value { return Str(dateISO(ms)) }))
	year, month, day, hour, minute, second, msec, wday := dateParts(ms)
	set := func(name string, v int) {
		o.set(name, builtinFn(name, func([]Value) Value { return Num(float64(v)) }))
	}
	set("getUTCFullYear", year)
	set("getUTCMonth", month-1)
	set("getUTCDate", day)
	set("getUTCDay", wday)
	set("getUTCHours", hour)
	set("getUTCMinutes", minute)
	set("getUTCSeconds", second)
	set("getUTCMilliseconds", msec)
	return nil
}

// dateArgTime reads the epoch milliseconds a Date constructor argument carries.
func dateArgTime(v Value) int64 {
	switch v.tag {
	case tNum:
		if math.IsNaN(v.n) || math.IsInf(v.n, 0) {
			return 0
		}
		return int64(v.n)
	case tObj:
		if ob := v.AsObj(); ob != nil {
			if ms := ob.get("ms"); ms.tag == tNum {
				return int64(ms.n)
			}
		}
	}
	return 0
}

// dateParts splits epoch milliseconds into the UTC calendar fields.
func dateParts(ms int64) (year, month, day, hour, minute, second, msec, wday int) {
	days := floorDiv(ms, msPerDay)
	rem := ms - days*msPerDay
	hour = int(rem / 3600000)
	rem -= int64(hour) * 3600000
	minute = int(rem / 60000)
	rem -= int64(minute) * 60000
	second = int(rem / 1000)
	msec = int(rem - int64(second)*1000)
	year, month, day = civilFromDays(days)
	wday = int((days%7 + 7 + 4) % 7)
	return
}

// floorDiv divides toward negative infinity.
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// civilFromDays converts days since the epoch to a proleptic Gregorian date
// (Howard Hinnant's algorithm).
func civilFromDays(z int64) (year, month, day int) {
	z += 719468
	era := z / 146097
	if z < 0 {
		era = (z - 146096) / 146097
	}
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y := int(yoe) + int(era)*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	day = int(doy - (153*mp+2)/5 + 1)
	month = int(mp + 3)
	if mp >= 10 {
		month = int(mp - 9)
	}
	if month <= 2 {
		y++
	}
	return y, month, day
}

// dateISO renders the ISO-8601 form of an instant.
func dateISO(ms int64) string {
	year, month, day, hour, minute, second, msec, _ := dateParts(ms)
	neg := ""
	if year < 0 {
		neg = "-"
		year = -year
	}
	return neg + pad4(year) + "-" + pad2(month) + "-" + pad2(day) + "T" +
		pad2(hour) + ":" + pad2(minute) + ":" + pad2(second) + "." + pad3(msec) + "Z"
}

func pad2(v int) string {
	if v < 10 {
		return "0" + strconv.Itoa(v)
	}
	return strconv.Itoa(v)
}

func pad3(v int) string {
	if v < 10 {
		return "00" + strconv.Itoa(v)
	}
	if v < 100 {
		return "0" + strconv.Itoa(v)
	}
	return strconv.Itoa(v)
}

func pad4(v int) string {
	if v < 10 {
		return "000" + strconv.Itoa(v)
	}
	if v < 100 {
		return "00" + strconv.Itoa(v)
	}
	if v < 1000 {
		return "0" + strconv.Itoa(v)
	}
	return strconv.Itoa(v)
}

// dateString is the default Date#toString: the UTC fields with a GMT suffix.
func dateString(ms int64) string {
	year, month, day, hour, minute, second, _, wday := dateParts(ms)
	const weekdays = "SunMonTueWedThuFriSat"
	const months = "JanFebMarAprMayJunJulAugSepOctNovDec"
	return weekdays[wday*3:wday*3+3] + " " + months[(month-1)*3:(month-1)*3+3] + " " +
		pad2(day) + " " + pad4(year) + " " + pad2(hour) + ":" + pad2(minute) + ":" + pad2(second) + " GMT+0000 (UTC)"
}

// dateUTC evaluates Date.UTC(y, m, d, h, min, s, ms).
func dateUTC(args []Value) int64 {
	at := func(i int) int64 {
		if i < len(args) && args[i].tag == tNum {
			return int64(args[i].n)
		}
		return 0
	}
	y := at(0)
	m := at(1)
	d := at(2)
	if d == 0 {
		d = 1
	}
	h, min, s, ms := at(3), at(4), at(5), at(6)
	// Shift January/February to the previous year so the day count is regular.
	if m > 11 {
		y += m / 12
		m = m % 12
	}
	if m < 0 {
		y -= (11 - m) / 12
		m = 12 + m%12
		if m == 12 {
			m = 0
		}
	}
	days := daysFromCivil(int(y), int(m)+1, int(d))
	return days*msPerDay + h*3600000 + min*60000 + s*1000 + ms
}

// daysFromCivil converts a proleptic Gregorian date to days since the epoch.
func daysFromCivil(y, m, d int) int64 {
	if m <= 2 {
		y--
	}
	era := y / 400
	if y < 0 {
		era = (y - 399) / 400
	}
	yoe := y - era*400
	mp := (m + 9) % 12
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return int64(era)*146097 + int64(doe) - 719468
}

// parseDateStr parses the ISO-8601 forms Date.parse accepts; anything else is
// 0, which is enough for the runtime's own uses.
func parseDateStr(s string) int64 {
	if s == "" {
		return 0
	}
	neg := false
	if s[0] == '+' || s[0] == '-' {
		neg = s[0] == '-'
		s = s[1:]
	}
	year := func(n int, text string) (int, bool) {
		v, err := strconv.Atoi(text)
		if err != nil || len(text) != n {
			return 0, false
		}
		return v, true
	}
	// Year only.
	if len(s) == 4 {
		y, ok := year(4, s)
		if !ok {
			return 0
		}
		if neg {
			y = -y
		}
		return daysFromCivil(y, 1, 1) * msPerDay
	}
	// yyyy-mm-dd[Thh:mm[:ss[.sss]]][Z]
	if len(s) < 10 || s[4] != '-' || s[7] != '-' {
		return 0
	}
	y, ok1 := year(4, s[0:4])
	mo, ok2 := year(2, s[5:7])
	d, ok3 := year(2, s[8:10])
	if !ok1 || !ok2 || !ok3 || mo < 1 || mo > 12 || d < 1 || d > 31 {
		return 0
	}
	if neg {
		y = -y
	}
	ms := daysFromCivil(y, mo, d) * msPerDay
	rest := s[10:]
	if rest == "" {
		return ms
	}
	if rest[0] != 'T' && rest[0] != ' ' {
		return 0
	}
	rest = rest[1:]
	if len(rest) < 5 || rest[2] != ':' {
		return 0
	}
	hh, ok1 := year(2, rest[0:2])
	mm, ok2 := year(2, rest[3:5])
	if !ok1 || !ok2 {
		return 0
	}
	ms += int64(hh)*3600000 + int64(mm)*60000
	rest = rest[5:]
	if len(rest) >= 3 && rest[0] == ':' {
		ss, ok := year(2, rest[1:3])
		if !ok {
			return 0
		}
		ms += int64(ss) * 1000
		rest = rest[3:]
	}
	if len(rest) >= 2 && rest[0] == '.' {
		end := 1
		for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
			end++
		}
		frac := rest[1:end]
		if len(frac) > 3 {
			frac = frac[:3]
		}
		for len(frac) < 3 {
			frac += "0"
		}
		ff, _ := strconv.Atoi(frac)
		ms += int64(ff)
		rest = rest[end:]
	}
	if rest != "" && rest != "Z" {
		return 0
	}
	return ms
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}
