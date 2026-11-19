package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"sboe/internal/normalize"
)

// AuctionRow es una subasta lista para mostrar (lectura de auctions_v).
type AuctionRow struct {
	ID                  int64
	PublicID            string
	AssetSubtype        string
	StatusCode          string
	ProvinceCode        string
	Source              string
	ExternalID          string
	AssetType           string
	AssetTypeRaw        string
	Status              string
	Locality            string
	Province            string
	StartDate           string
	EndDate             string
	ValuationCents      *int64
	AppraisalCents      *int64
	MinBidCents         *int64
	DepositCents        *int64
	ClaimedCents        *int64
	ChargesCents        *int64
	Description         string
	MainResidence       *bool
	URL                 string
	Extra               map[string]string
	FirstSeen           time.Time
	Active              bool
	PctClaimedValuation *float64
}

const auctionCols = `id, source, external_id, COALESCE(asset_type,''), COALESCE(asset_type_raw,''), COALESCE(status,''),
	COALESCE(locality,''), COALESCE(province,''), COALESCE(start_date,''), COALESCE(end_date,''),
	valuation_cents, appraisal_cents, min_bid_cents, deposit_cents, claimed_cents, charges_cents,
	COALESCE(description,''), main_residence, url, COALESCE(extra,'{}'), first_seen, active, pct_claimed_valuation,
	public_id, COALESCE(asset_subtype,''), COALESCE(status_code,''), COALESCE(province_code,'')`

func scanAuction(rows *sql.Rows) (AuctionRow, error) {
	var a AuctionRow
	var extra, first string
	var mr sql.NullBool
	err := rows.Scan(&a.ID, &a.Source, &a.ExternalID, &a.AssetType, &a.AssetTypeRaw, &a.Status, &a.Locality, &a.Province,
		&a.StartDate, &a.EndDate, &a.ValuationCents, &a.AppraisalCents, &a.MinBidCents, &a.DepositCents, &a.ClaimedCents,
		&a.ChargesCents, &a.Description, &mr, &a.URL, &extra, &first, &a.Active, &a.PctClaimedValuation,
		&a.PublicID, &a.AssetSubtype, &a.StatusCode, &a.ProvinceCode)
	if err != nil {
		return a, err
	}
	if mr.Valid {
		a.MainResidence = &mr.Bool
	}
	_ = json.Unmarshal([]byte(extra), &a.Extra)
	a.FirstSeen, _ = time.Parse(TSFormat, first)
	return a, nil
}

// QueryAuctions busca subastas. where/args vienen de filter.Build; orderBy de filter.OrderBy.
func (s *Store) QueryAuctions(ctx context.Context, where string, args []any, orderBy string, limit, offset int) ([]AuctionRow, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM auctions_v WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT ` + auctionCols + ` FROM auctions_v WHERE ` + where + ` ORDER BY ` + orderBy + ` LIMIT ? OFFSET ?`
	rows, err := s.db.QueryContext(ctx, q, append(append([]any{}, args...), limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []AuctionRow
	for rows.Next() {
		a, err := scanAuction(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

// ProvinceCounts devuelve cuántas subastas activas hay por código INE de provincia.
func (s *Store) ProvinceCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT province_code, count(*) FROM auctions WHERE active = 1 AND province_code IS NOT NULL GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var code string
		var n int
		if err := rows.Scan(&code, &n); err != nil {
			return nil, err
		}
		out[code] = n
	}
	return out, rows.Err()
}

// AuctionStats: totales activos por fuente.
func (s *Store) AuctionStats(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source, count(*) FROM auctions WHERE active = 1 GROUP BY source`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var src string
		var n int
		if err := rows.Scan(&src, &n); err != nil {
			return nil, err
		}
		out[src] = n
	}
	return out, rows.Err()
}

// LocalityOption es una localidad con subastas activas, para sugerirla en el filtro.
type LocalityOption struct {
	Name     string
	Province string
	Count    int
}

// LocalityOptions lista las localidades de las subastas activas (agrupando mayúsculas y
// tildes), de más a menos subastas. Se alimenta sola a medida que entran subastas.
// Como nombre se queda con la grafía más cuidada (mayúsculas y minúsculas, tildes) y como
// provincia la más repetida.
func (s *Store) LocalityOptions(ctx context.Context) ([]LocalityOption, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT norm(locality), trim(locality), COALESCE(province,''), count(*)
		FROM auctions WHERE active = 1 AND COALESCE(trim(locality),'') <> ''
		GROUP BY 1, 2, 3`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type agg struct {
		LocalityOption
		key       string
		provCount map[string]int // subastas por provincia ("" = sin provincia)
	}
	byKey := map[string]*agg{}
	for rows.Next() {
		var key, name, prov string
		var n int
		if err := rows.Scan(&key, &name, &prov, &n); err != nil {
			return nil, err
		}
		a := byKey[key]
		if a == nil {
			a = &agg{key: key, provCount: map[string]int{}}
			a.Name = name
			byKey[key] = a
		}
		a.Count += n
		a.provCount[prov] += n
		if prov != "" && a.provCount[prov] > a.provCount[a.Province] {
			a.Province = prov
		}
		if nameScore(name) > nameScore(a.Name) {
			a.Name = name
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Catálogo fijo: siempre aparecen (aunque tengan 0 subastas) y suman todas sus grafías,
	// pero solo las subastas de su provincia (La Granada, Barcelona ≠ Granada).
	var all []*agg
	for _, k := range normalize.KnownLocalities() {
		prov := ""
		if p, ok := normalize.ProvinceByCode(k.ProvinceCode); ok {
			prov = p.Name
		}
		total := 0
		for _, key := range k.Keys() {
			a := byKey[key]
			if a == nil {
				continue
			}
			for _, pv := range []string{prov, ""} {
				total += a.provCount[pv]
				a.Count -= a.provCount[pv]
				delete(a.provCount, pv)
			}
			if a.Count <= 0 {
				delete(byKey, key)
				continue
			}
			a.Province = ""
			for pv, n := range a.provCount {
				if n > a.provCount[a.Province] {
					a.Province = pv
				}
			}
		}
		for _, name := range k.Names {
			o := &agg{key: normalize.Text(name)}
			o.Name, o.Province, o.Count = name, prov, total
			all = append(all, o)
		}
	}
	for _, a := range byKey {
		all = append(all, a)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Count != all[j].Count {
			return all[i].Count > all[j].Count
		}
		return all[i].key < all[j].key
	})
	out := make([]LocalityOption, len(all))
	for i, a := range all {
		out[i] = a.LocalityOption
	}
	return out, nil
}

// nameScore puntúa una grafía: mejor con mayúsculas y minúsculas, y mejor con tildes.
func nameScore(s string) int {
	n := 0
	if s != strings.ToUpper(s) && s != strings.ToLower(s) {
		n += 2
	}
	for _, r := range s {
		if r > 127 {
			n++
			break
		}
	}
	return n
}
