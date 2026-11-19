package normalize

import "testing"

func TestLocalityFromAddress(t *testing.T) {
	cases := map[string]string{
		"CL FONT DE LA VIA (RUBÍ)":                              "RUBÍ",
		"CL RUPERTO URQUIJO (LAUDIO/LLODIO)":                    "LAUDIO/LLODIO",
		"CL FRAY FRANCISCO SOBRECASAS (PUEBLA DE ALFINDÉN, LA)": "LA PUEBLA DE ALFINDÉN",
		"CL TENERIAS, (CABRERO)":                                "CABRERO",
		"CM LA ORCHILLA-ZOCAS-TF 647 (38617)":                   "",
		"CL MAYOR":                                              "",
	}
	for in, want := range cases {
		if got := LocalityFromAddress(in); got != want {
			t.Errorf("LocalityFromAddress(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDisplayName(t *testing.T) {
	cases := map[string]string{
		"SAN ANDRÉS DEL RABANEDO": "San Andrés del Rabanedo",
		"LAUDIO/LLODIO":           "Laudio/Llodio",
		"Mahón":                   "Mahón",
		"jerez de la frontera":    "Jerez de la Frontera",
	}
	for in, want := range cases {
		if got := DisplayName(in); got != want {
			t.Errorf("DisplayName(%q) = %q, want %q", in, got, want)
		}
	}
}
