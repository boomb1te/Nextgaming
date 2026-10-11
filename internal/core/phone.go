package core

// Phone numbers in NextGaming are Turkmen (+993) only. Clients are stored in a
// canonical "+993" + 8-digit form, and matched against an incoming call by the
// 8-digit local part, so formatting differences ("+993 65 12 34 56", "065123456",
// "65123456") never stop a known caller from being recognised.

// NormalizeTMPhone turns any reasonable spelling of a Turkmen number into its
// canonical form ("+99365123456") and its 8-digit local key ("65123456").
// ok is false when the input has fewer than 8 digits (or an implausible length),
// in which case it is not a usable phone number.
func NormalizeTMPhone(raw string) (canonical, key string, ok bool) {
	digits := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		if c := raw[i]; c >= '0' && c <= '9' {
			digits = append(digits, c)
		}
	}
	// A Turkmen local number is 8 digits; we accept any longer spelling (with a
	// country code 993, a trunk 0/8, spaces, +) and key on the last 8 digits.
	if len(digits) < 8 || len(digits) > 15 {
		return "", "", false
	}
	key = string(digits[len(digits)-8:])
	return "+993" + key, key, true
}

// PhoneKey returns just the 8-digit local key for a number, or "" if it is not
// a usable Turkmen number.
func PhoneKey(raw string) string {
	_, key, ok := NormalizeTMPhone(raw)
	if !ok {
		return ""
	}
	return key
}
