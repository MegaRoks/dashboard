package tgbot

import "strings"

// DescribeUserAgent превращает User-Agent в «Chrome · macOS» для сообщения о входе.
// Точность не важна: это подсказка, чтобы узнать своё устройство.
func DescribeUserAgent(ua string) string {
	browser := firstMatch(ua, [][2]string{
		{"YaBrowser/", "Яндекс Браузер"}, {"Edg/", "Edge"}, {"OPR/", "Opera"}, {"Firefox/", "Firefox"},
		{"CriOS/", "Chrome"}, {"Chrome/", "Chrome"}, {"Safari/", "Safari"}, {"curl/", "curl"},
	})
	os := firstMatch(ua, [][2]string{
		{"iPhone", "iOS"}, {"iPad", "iPadOS"}, {"Android", "Android"}, {"Windows", "Windows"},
		{"Mac OS X", "macOS"}, {"CrOS", "ChromeOS"}, {"Linux", "Linux"},
	})
	switch {
	case browser != "" && os != "":
		return browser + " · " + os
	case browser != "" || os != "":
		return browser + os
	default:
		return "неизвестное устройство"
	}
}

func firstMatch(s string, rules [][2]string) string {
	for _, r := range rules {
		if strings.Contains(s, r[0]) {
			return r[1]
		}
	}
	return ""
}
