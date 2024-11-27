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
	sb := strings.Builder{}
	for _, c := range s {
		if (c >= 0 && c < 32) || (c >= 127 && c <= 255) {
			sb.WriteString(fmt.Sprintf("\\%03o", c))
		} else if c == '"' {
			sb.WriteString(`\"`)
		} else if c == '\\' {
			sb.WriteString(`\\`)
		} else {
			sb.WriteRune(c)
		}
	}
	s = sb.String()

	// Quote strings
	s = `"` + s + `"`

	return s
}

func unquote(s string) string {
	// Unescape special characters
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := rune(s[i])
		if c == '\\' && len(s) > i+1 {
			if s[i+1] == '"' {
				sb.WriteRune('"')
				i++
				continue
			} else if s[i+1] == '\\' {
				sb.WriteRune('\\')
				i++
				continue
			} else if s[i+1] >= '0' && s[i+1] <= '7' && len(s) > i+3 {
				octal, err := strconv.ParseInt(s[i+1:i+4], 8, 32)
				if err == nil {
					sb.WriteRune(rune(octal))
					i += 3
					continue
				}
			}
		}
		sb.WriteRune(c)
	}

	return strings.Trim(sb.String(), `"`)
}
