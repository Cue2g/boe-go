package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
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
