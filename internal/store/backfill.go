package store

import (
	"database/sql"
	"time"

	"sboe/internal/model"
	"sboe/internal/normalize"
)

// migrationHooks: código Go que se ejecuta dentro de la transacción de una migración,
// después de su SQL.
var migrationHooks = map[int]func(*sql.Tx) error{
	3: backfillNormalized,
	4: backfillLocality,
}

// backfillLocality rellena la localidad de las subastas ya guardadas que no la tienen:
// en Seguridad Social (regulares) sale de la dirección "CL MAYOR (RUBÍ)"; en el resto,
// si la descripción nombra una localidad del catálogo (normalize.KnownLocalities).
func backfillLocality(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT id, source, COALESCE(asset_type,''), COALESCE(description,''), COALESCE(province_code,'')
		FROM auctions WHERE COALESCE(trim(locality),'') = ''`)
	if err != nil {
		return err
	}
	type fix struct{ loc, provCode, prov string }
	found := map[int64]fix{}
	for rows.Next() {
		var id int64
		var source, assetType, desc, provCode string
		if err := rows.Scan(&id, &source, &assetType, &desc, &provCode); err != nil {
			rows.Close()
			return err
		}
		f := fix{provCode: provCode}
		if source == "ss_regulares" && assetType == "inmueble" {
			f.loc = normalize.LocalityFromAddress(desc)
		}
		if f.loc == "" {
			if k, ok := normalize.KnownLocalityIn(desc, provCode); ok {
				f.loc = k.Names[0]
				if provCode == "" {
					if p, ok := normalize.ProvinceByCode(k.ProvinceCode); ok {
						f.provCode, f.prov = p.Code, p.Name
					}
				}
			}
		}
		if f.loc != "" {
			found[id] = f
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for id, f := range found {
		if _, err := tx.Exec(`UPDATE auctions SET locality = ?, province_code = COALESCE(NULLIF(?, ''), province_code),
			province = COALESCE(NULLIF(?, ''), province) WHERE id = ?`, f.loc, f.provCode, f.prov, id); err != nil {
			return err
		}
	}
	return nil
}

// backfillNormalized rellena los campos unificados de las subastas ya guardadas.
func backfillNormalized(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT id, source, COALESCE(asset_type,''), COALESCE(asset_type_raw,''), COALESCE(status,''),
		COALESCE(locality,''), COALESCE(province,''), COALESCE(start_date,''), COALESCE(end_date,''), COALESCE(description,'')
		FROM auctions`)
	if err != nil {
		return err
	}
	type row struct {
		id int64
		a  model.Auction
	}
	var all []row
	for rows.Next() {
		var r row
		var start, end string
		if err := rows.Scan(&r.id, &r.a.Source, &r.a.AssetType, &r.a.AssetTypeRaw, &r.a.Status, &r.a.Locality, &r.a.Province, &start, &end, &r.a.Description); err != nil {
			rows.Close()
			return err
		}
		if t, err := time.Parse("2006-01-02", start); err == nil {
			r.a.StartDate = &t
		}
		if t, err := time.Parse("2006-01-02", end); err == nil {
			r.a.EndDate = &t
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`UPDATE auctions SET asset_type=?, asset_subtype=?, status_code=?, province=?, province_code=?, province_raw=? WHERE id=?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	now := time.Now()
	for _, r := range all {
		normalize.Enrich(&r.a, now)
		if _, err := stmt.Exec(r.a.AssetType, nullStr(r.a.AssetSubtype), nullStr(r.a.StatusCode), nullStr(r.a.Province),
			nullStr(r.a.ProvinceCode), nullStr(r.a.ProvinceRaw), r.id); err != nil {
			return err
		}
	}
	return nil
}
