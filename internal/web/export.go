package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"sboe/internal/auth"
	"sboe/internal/filter"
	"sboe/internal/store"
)

// exportLimit es el máximo de filas de una descarga.
const exportLimit = 10000

type exportCol struct {
	title string
	width float64
	kind  int // colText, colMoney, colPct
	value func(a store.AuctionRow) any
}

const (
	colText = iota
	colMoney
	colPct
)

func labelOf(m map[string]string, k string) string {
	if l, ok := m[k]; ok {
		return l
	}
	return k
}

func cents(c *int64) any {
	if c == nil {
		return nil
	}
	return float64(*c) / 100
}

func exportCols(loc *time.Location) []exportCol {
	return []exportCol{
		{"Plataforma", 22, colText, func(a store.AuctionRow) any { return labelOf(filter.SourceLabel, a.Source) }},
		{"ID", 12, colText, func(a store.AuctionRow) any { return a.PublicID }},
		{"Referencia", 24, colText, func(a store.AuctionRow) any { return a.ExternalID }},
		{"Tipo", 12, colText, func(a store.AuctionRow) any { return labelOf(filter.AssetLabel, a.AssetType) }},
		{"Subtipo", 14, colText, func(a store.AuctionRow) any { return labelOf(filter.SubtypeLabel, a.AssetSubtype) }},
		{"Estado", 22, colText, func(a store.AuctionRow) any { return labelOf(filter.StatusLabel, a.StatusCode) }},
		{"Localidad", 22, colText, func(a store.AuctionRow) any { return a.Locality }},
		{"Provincia", 16, colText, func(a store.AuctionRow) any { return a.Province }},
		{"Inicio", 12, colText, func(a store.AuctionRow) any { return a.StartDate }},
		{"Fin", 12, colText, func(a store.AuctionRow) any { return a.EndDate }},
		{"Valor de subasta", 16, colMoney, func(a store.AuctionRow) any { return cents(a.ValuationCents) }},
		{"Tasación", 16, colMoney, func(a store.AuctionRow) any { return cents(a.AppraisalCents) }},
		{"Puja mínima", 16, colMoney, func(a store.AuctionRow) any { return cents(a.MinBidCents) }},
		{"Depósito", 14, colMoney, func(a store.AuctionRow) any { return cents(a.DepositCents) }},
		{"Reclamado", 16, colMoney, func(a store.AuctionRow) any { return cents(a.ClaimedCents) }},
		{"Cargas", 14, colMoney, func(a store.AuctionRow) any { return cents(a.ChargesCents) }},
		{"% reclamado", 12, colPct, func(a store.AuctionRow) any {
			if a.PctClaimedValuation == nil {
				return nil
			}
			return *a.PctClaimedValuation / 100
		}},
		{"Vivienda habitual", 10, colText, func(a store.AuctionRow) any {
			if a.MainResidence == nil {
				return ""
			}
			if *a.MainResidence {
				return "Sí"
			}
			return "No"
		}},
		{"Publicada", 10, colText, func(a store.AuctionRow) any {
			if a.Active {
				return "Sí"
			}
			return "No"
		}},
		{"Detectada", 17, colText, func(a store.AuctionRow) any { return a.FirstSeen.In(loc).Format("2006-01-02 15:04") }},
		{"Descripción", 60, colText, func(a store.AuctionRow) any { return a.Description }},
		{"Enlace", 14, colText, nil}, // hipervínculo, se rellena aparte
	}
}

// auctionsExport descarga en .xlsx todas las subastas que cumplen los filtros actuales (mismo orden).
func (s *Server) auctionsExport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	c := filter.FromValues(q)
	where, args := filter.Build(c)
	items, _, err := s.Store.QueryAuctions(r.Context(), where, args, filter.OrderBy(q.Get("sort")), exportLimit, 0)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	f, err := buildXLSX(items, s.Location)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer f.Close()
	name := "subastas-" + time.Now().In(s.Location).Format("2006-01-02") + ".xlsx"
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	if err := f.Write(w); err != nil {
		u, _ := auth.UserFrom(r.Context())
		slog.Error("export xlsx", "user", u.ID, "err", err)
	}
}

func buildXLSX(items []store.AuctionRow, loc *time.Location) (*excelize.File, error) {
	const sheet = "Subastas"
	f := excelize.NewFile()
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		return nil, err
	}
	head, err := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "2E322B"},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"A2AF9B"}},
		Alignment: &excelize.Alignment{Vertical: "center"},
	})
	if err != nil {
		return nil, err
	}
	moneyFmt := `#,##0.00 "€"`
	money, _ := f.NewStyle(&excelize.Style{CustomNumFmt: &moneyFmt})
	pctFmt := "0.0%"
	pct, _ := f.NewStyle(&excelize.Style{CustomNumFmt: &pctFmt})
	link, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Color: "4F5C49", Underline: "single"}})

	cols := exportCols(loc)
	sw, err := f.NewStreamWriter(sheet)
	if err != nil {
		return nil, err
	}
	for i, col := range cols {
		if err := sw.SetColWidth(i+1, i+1, col.width); err != nil {
			return nil, err
		}
	}
	if err := sw.SetPanes(&excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"}); err != nil {
		return nil, err
	}
	row := make([]any, len(cols))
	for i, col := range cols {
		row[i] = excelize.Cell{StyleID: head, Value: col.title}
	}
	if err := sw.SetRow("A1", row, excelize.RowOpts{Height: 22}); err != nil {
		return nil, err
	}
	for n, a := range items {
		row := make([]any, len(cols))
		for i, col := range cols {
			if col.value == nil { // enlace
				if a.URL != "" && len(a.URL) < 240 && !strings.Contains(a.URL, `"`) {
					row[i] = excelize.Cell{StyleID: link, Formula: fmt.Sprintf(`HYPERLINK("%s","Ver subasta")`, a.URL), Value: "Ver subasta"}
				} else {
					row[i] = a.URL
				}
				continue
			}
			v := col.value(a)
			switch col.kind {
			case colMoney:
				row[i] = excelize.Cell{StyleID: money, Value: v}
			case colPct:
				row[i] = excelize.Cell{StyleID: pct, Value: v}
			default:
				row[i] = v
			}
		}
		cell, _ := excelize.CoordinatesToCellName(1, n+2)
		if err := sw.SetRow(cell, row); err != nil {
			return nil, err
		}
	}
	// Tabla de Excel: botones de filtro/orden en la cabecera y filas cebra.
	if len(items) > 0 {
		last, _ := excelize.CoordinatesToCellName(len(cols), len(items)+1)
		stripes := true
		if err := sw.AddTable(&excelize.Table{Range: "A1:" + last, Name: "Subastas", StyleName: "TableStyleLight1", ShowRowStripes: &stripes}); err != nil {
			return nil, err
		}
	}
	if err := sw.Flush(); err != nil {
		return nil, err
	}
	return f, nil
}
