// Package tzutil resolves IANA timezone names, normalizing the legacy spellings
// that old cron tooling still sends (Europe/Kiev, Asia/Calcutta, US/Pacific…).
package tzutil

import (
	"time"

	_ "time/tzdata"
)

// Normalize maps a legacy zone name to its current canonical name.
func Normalize(name string) string {
	if canonical, ok := legacy[name]; ok {
		return canonical
	}
	return name
}

// Load resolves a timezone name, accepting legacy aliases. An empty name means UTC.
func Load(name string) (*time.Location, error) {
	if name == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(Normalize(name))
	if err != nil {
		return nil, &UnknownZoneError{Name: name}
	}
	return loc, nil
}

type UnknownZoneError struct {
	Name string
}

func (e *UnknownZoneError) Error() string {
	return "未知的时区名称：" + e.Name
}

// Valid reports whether name resolves to a loadable zone.
func Valid(name string) bool {
	if name == "" {
		return true
	}
	_, err := Load(name)
	return err == nil
}

var legacy = map[string]string{
	"Africa/Asmera":                    "Africa/Nairobi",
	"America/Argentina/ComodRivadavia": "America/Argentina/Catamarca",
	"America/Buenos_Aires":             "America/Argentina/Buenos_Aires",
	"America/Catamarca":                "America/Argentina/Catamarca",
	"America/Cordoba":                  "America/Argentina/Cordoba",
	"America/Fort_Wayne":               "America/Indiana/Indianapolis",
	"America/Godthab":                  "America/Nuuk",
	"America/Indianapolis":             "America/Indiana/Indianapolis",
	"America/Jujuy":                    "America/Argentina/Jujuy",
	"America/Knox_IN":                  "America/Indiana/Knox",
	"America/Louisville":               "America/Kentucky/Louisville",
	"America/Mendoza":                  "America/Argentina/Mendoza",
	"America/Rosario":                  "America/Argentina/Cordoba",
	"Antarctica/South_Pole":            "Pacific/Auckland",
	"Asia/Ashkhabad":                   "Asia/Ashgabat",
	"Asia/Calcutta":                    "Asia/Kolkata",
	"Asia/Choibalsan":                  "Asia/Ulaanbaatar",
	"Asia/Chungking":                   "Asia/Shanghai",
	"Asia/Dacca":                       "Asia/Dhaka",
	"Asia/Katmandu":                    "Asia/Kathmandu",
	"Asia/Macao":                       "Asia/Macau",
	"Asia/Rangoon":                     "Asia/Yangon",
	"Asia/Saigon":                      "Asia/Ho_Chi_Minh",
	"Asia/Thimbu":                      "Asia/Thimphu",
	"Asia/Ujung_Pandang":               "Asia/Makassar",
	"Asia/Ulan_Bator":                  "Asia/Ulaanbaatar",
	"Atlantic/Faeroe":                  "Atlantic/Faroe",
	"Australia/ACT":                    "Australia/Sydney",
	"Australia/LHI":                    "Australia/Lord_Howe",
	"Australia/North":                  "Australia/Darwin",
	"Australia/NSW":                    "Australia/Sydney",
	"Australia/Queensland":             "Australia/Brisbane",
	"Australia/South":                  "Australia/Adelaide",
	"Australia/Tasmania":               "Australia/Hobart",
	"Australia/Victoria":               "Australia/Melbourne",
	"Australia/West":                   "Australia/Perth",
	"Brazil/Acre":                      "America/Rio_Branco",
	"Brazil/DeNoronha":                 "America/Noronha",
	"Brazil/East":                      "America/Sao_Paulo",
	"Brazil/West":                      "America/Manaus",
	"Canada/Atlantic":                  "America/Halifax",
	"Canada/Central":                   "America/Winnipeg",
	"Canada/Eastern":                   "America/Toronto",
	"Canada/Mountain":                  "America/Edmonton",
	"Canada/Newfoundland":              "America/St_Johns",
	"Canada/Pacific":                   "America/Vancouver",
	"Canada/Saskatchewan":              "America/Regina",
	"Canada/Yukon":                     "America/Whitehorse",
	"CET":                              "Europe/Brussels",
	"Chile/Continental":                "America/Santiago",
	"Chile/EasterIsland":               "Pacific/Easter",
	"CST6CDT":                          "America/Chicago",
	"Cuba":                             "America/Havana",
	"EET":                              "Europe/Athens",
	"Egypt":                            "Africa/Cairo",
	"Eire":                             "Europe/Dublin",
	"EST":                              "America/Panama",
	"EST5EDT":                          "America/New_York",
	"Europe/Kiev":                      "Europe/Kyiv",
	"Europe/Uzhgorod":                  "Europe/Kyiv",
	"Europe/Zaporozhye":                "Europe/Kyiv",
	"GB":                               "Europe/London",
	"GB-Eire":                          "Europe/London",
	"GMT+0":                            "Etc/GMT",
	"GMT-0":                            "Etc/GMT",
	"GMT0":                             "Etc/GMT",
	"Greenwich":                        "Etc/GMT",
	"Hongkong":                         "Asia/Hong_Kong",
	"HST":                              "Pacific/Honolulu",
	"Iceland":                          "Africa/Abidjan",
	"Iran":                             "Asia/Tehran",
	"Israel":                           "Asia/Jerusalem",
	"Jamaica":                          "America/Jamaica",
	"Japan":                            "Asia/Tokyo",
	"Kwajalein":                        "Pacific/Kwajalein",
	"Libya":                            "Africa/Tripoli",
	"MET":                              "Europe/Brussels",
	"Mexico/BajaNorte":                 "America/Tijuana",
	"Mexico/BajaSur":                   "America/Mazatlan",
	"Mexico/General":                   "America/Mexico_City",
	"MST":                              "America/Phoenix",
	"MST7MDT":                          "America/Denver",
	"Navajo":                           "America/Denver",
	"NZ":                               "Pacific/Auckland",
	"NZ-CHAT":                          "Pacific/Chatham",
	"Pacific/Enderbury":                "Pacific/Kanton",
	"Pacific/Ponape":                   "Pacific/Guadalcanal",
	"Pacific/Truk":                     "Pacific/Port_Moresby",
	"Poland":                           "Europe/Warsaw",
	"Portugal":                         "Europe/Lisbon",
	"PRC":                              "Asia/Shanghai",
	"PST8PDT":                          "America/Los_Angeles",
	"ROC":                              "Asia/Taipei",
	"ROK":                              "Asia/Seoul",
	"Singapore":                        "Asia/Singapore",
	"Turkey":                           "Europe/Istanbul",
	"UCT":                              "Etc/UTC",
	"Universal":                        "Etc/UTC",
	"US/Alaska":                        "America/Anchorage",
	"US/Aleutian":                      "America/Adak",
	"US/Arizona":                       "America/Phoenix",
	"US/Central":                       "America/Chicago",
	"US/East-Indiana":                  "America/Indiana/Indianapolis",
	"US/Eastern":                       "America/New_York",
	"US/Hawaii":                        "Pacific/Honolulu",
	"US/Indiana-Starke":                "America/Indiana/Knox",
	"US/Michigan":                      "America/Detroit",
	"US/Mountain":                      "America/Denver",
	"US/Pacific":                       "America/Los_Angeles",
	"US/Samoa":                         "Pacific/Pago_Pago",
	"W-SU":                             "Europe/Moscow",
	"WET":                              "Europe/Lisbon",
	"Zulu":                             "Etc/UTC",
}

// ValidateName checks the shape before the (potentially slow) filesystem lookup.
func ValidateName(name string) bool {
	if name == "" || len(name) > 36 {
		return name == ""
	}
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9',
			r == '_', r == '/', r == '+', r == '-':
		default:
			return false
		}
	}
	return true
}
