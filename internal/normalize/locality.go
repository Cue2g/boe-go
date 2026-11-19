package normalize

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	reTrailingParen = regexp.MustCompile(`\(([^()]+)\)\s*$`)
	// "PUEBLA DE ALFINDÉN, LA" -> "LA PUEBLA DE ALFINDÉN"
	reArticleSuffix = regexp.MustCompile(`(?i)^(.+?),\s*(la|el|los|las|l'|els|les|es|sa|ses|o|a|os|as)$`)
)

// LocalityFromAddress saca la localidad de una dirección tipo "CL MAYOR 3 (RUBÍ)".
// Devuelve "" si no hay paréntesis final o lo que hay dentro no parece un municipio
// (códigos postales, referencias numéricas…).
func LocalityFromAddress(s string) string {
	m := reTrailingParen.FindStringSubmatch(Clean(s))
	if m == nil {
		return ""
	}
	loc := strings.Trim(strings.TrimSpace(m[1]), ",. ")
	letters := 0
	for _, r := range loc {
		if unicode.IsDigit(r) {
			return ""
		}
		if unicode.IsLetter(r) {
			letters++
		}
	}
	if letters < 2 {
		return ""
	}
	if a := reArticleSuffix.FindStringSubmatch(loc); a != nil {
		sep := " "
		if strings.HasSuffix(a[2], "'") {
			sep = ""
		}
		loc = a[2] + sep + a[1]
	}
	return loc
}

// DisplayName pone en formato título un nombre que viene todo en mayúsculas o todo en
// minúsculas ("SAN ANDRÉS DEL RABANEDO" -> "San Andrés del Rabanedo"). Si ya viene mixto, lo deja.
func DisplayName(s string) string {
	if s != strings.ToUpper(s) && s != strings.ToLower(s) {
		return s
	}
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		if i > 0 && minorWords[w] {
			continue
		}
		words[i] = capitalize(w)
	}
	return strings.Join(words, " ")
}

var minorWords = map[string]bool{"de": true, "del": true, "la": true, "las": true, "el": true, "los": true,
	"y": true, "i": true, "d'": true, "l'": true, "en": true}

func capitalize(w string) string {
	out := []rune(w)
	up := true
	for i, r := range out {
		if up && unicode.IsLetter(r) {
			out[i] = unicode.ToUpper(r)
			up = false
		}
		if r == '-' || r == '/' || r == '(' {
			up = true
		}
	}
	return string(out)
}
