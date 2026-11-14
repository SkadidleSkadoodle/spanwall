package display

import "strings"

// brands maps common EDID maker codes to the name people use.
var brands = map[string]string{
	"ACR": "Acer", "AUO": "AU Optronics", "AUS": "Asus", "BNQ": "BenQ", "BOE": "BOE",
	"CMN": "Chimei Innolux", "DEL": "Dell", "GSM": "LG", "HWP": "HP", "HPN": "HP",
	"IVM": "Iiyama", "LEN": "Lenovo", "LGD": "LG Display", "MSI": "MSI", "PHL": "Philips",
	"SAM": "Samsung", "SDC": "Samsung Display", "SHP": "Sharp", "SNY": "Sony", "VSC": "ViewSonic",
	"GBT": "Gigabyte", "AOC": "AOC", "APP": "Apple", "MEI": "Panasonic", "NEC": "NEC",
}

// Brand returns the brand for a stable ID like "ACR-1060-...", or "".
func Brand(id string) string {
	code, _, _ := strings.Cut(id, "-")
	return brands[code]
}
