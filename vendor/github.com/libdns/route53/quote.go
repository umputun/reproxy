package route53

import (
	"fmt"
	"strconv"
	"strings"
)

func quote(s string) string {
	// Special characters in a TXT record value
	//
	// If your TXT record contains any of the following characters, you must specify the characters by using escape codes in the format \three-digit octal code:
	// Characters 000 to 040 octal (0 to 32 decimal, 0x00 to 0x20 hexadecimal)
	// Characters 177 to 377 octal (127 to 255 decimal, 0x7F to 0xFF hexadecimal)
	// ...
	// for example, if the value of your TXT record is "exämple.com", you specify "ex\344mple.com".
	// source https://docs.aws.amazon.com/Route53/latest/DeveloperGuide/ResourceRecordTypes.html#TXTFormat
	sb := strings.Builder{}
	for i := range len(s) {
		c := s[i]
		switch {
		case c < 32 || c >= 127:
			_, _ = fmt.Fprintf(&sb, "\\%03o", c)
		case c == '"':
			sb.WriteString(`\"`)
		case c == '\\':
			sb.WriteString(`\\`)
		default:
			sb.WriteByte(c)
		}
	}
	s = sb.String()

	// quote strings
	s = `"` + s + `"`

	return s
}

func unquote(s string) string {
	// Unescape special characters
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := rune(s[i])
		if c == '\\' && len(s) > i+1 {
			switch {
			case s[i+1] == '"':
				sb.WriteRune('"')
				i++
				continue
			case s[i+1] == '\\':
				sb.WriteRune('\\')
				i++
				continue
			case s[i+1] >= '0' && s[i+1] <= '7' && len(s) > i+3:
				// Route53 TXT octal escapes are 8-bit (0-255). ParseUint with
				// bitSize=8 returns an error for any 3-digit octal that
				// overflows a byte (e.g. \400+), in which case we leave the
				// backslash unescaped — matches the prior fall-through behavior.
				octal, err := strconv.ParseUint(s[i+1:i+4], 8, 8)
				if err == nil {
					sb.WriteByte(byte(octal))
					i += 3
					continue
				}
			}
		}
		sb.WriteRune(c)
	}

	return strings.Trim(sb.String(), `"`)
}
