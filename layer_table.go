package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	// Database
	"github.com/CrunchyData/pg_tileserv/cql"
	"github.com/jackc/pgtype"

	// Logging
	log "github.com/sirupsen/logrus"

	// Configuration
	"github.com/spf13/viper"
)

// RasterOverview holds overview metadata for rasters
type RasterOverview struct {
	TableSchema    string `json:"o_table_schema"`
	TableName      string `json:"o_table_name"`
	RasterColumn   string `json:"o_raster_column"`
	OverviewFactor int    `json:"overview_factor"`
}

// LayerTable provides metadata about the table layer
type LayerTable struct {
	ID              string
	Schema          string
	Table           string
	Description     string
	Properties      map[string]TableProperty
	GeometryType    string
	IDColumn        string
	GeometryColumn  string
	Srid            int
	RasterOverviews []RasterOverview
}

// TableProperty provides metadata about a single property field,
// features in a table layer may have multiple such fields
type TableProperty struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	order       int
}

// TableDetailJSON gives the output structure for the table layer.
type TableDetailJSON struct {
	ID           string          `json:"id"`
	Schema       string          `json:"schema"`
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	Properties   []TableProperty `json:"properties,omitempty"`
	GeometryType string          `json:"geometrytype,omitempty"`
	Center       [2]float64      `json:"center"`
	Bounds       [4]float64      `json:"bounds"`
	MinZoom      int             `json:"minzoom"`
	MaxZoom      int             `json:"maxzoom"`
	TileURL      string          `json:"tileurl"`
}

/********************************************************************************
 * Layer Interface
 */

// GetType disambiguates between function and table layers
func (lyr LayerTable) GetType() LayerType {
	return LayerTypeTable
}

// GetID returns the complete ID (schema.name) by which to reference a given layer
func (lyr LayerTable) GetID() string {
	return lyr.ID
}

// GetDescription returns the text description for a layer
// or an empty string if no description is set
func (lyr LayerTable) GetDescription() string {
	return lyr.Description
}

// GetName returns just the name of a given layer
func (lyr LayerTable) GetName() string {
	return lyr.Table
}

// GetSchema returns just the schema for a given layer
func (lyr LayerTable) GetSchema() string {
	return lyr.Schema
}

// WriteLayerJSON outputs parameters and optional arguments for the table layer
func (lyr LayerTable) WriteLayerJSON(w http.ResponseWriter, req *http.Request) error {
	jsonTableDetail, err := lyr.getTableDetailJSON(req)
	if err != nil {
		return err
	}
	w.Header().Add("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jsonTableDetail)
	// all good, no error
	return nil
}

// GetTileRequest takes tile and request parameters as input and returns a TileRequest
// specifying the SQL to fetch appropriate data
func (lyr LayerTable) GetTileRequest(tile Tile, r *http.Request) TileRequest {
	rp := lyr.getQueryParameters(r.URL.Query())
	sql, _ := lyr.requestSQL(&tile, &rp)

	tr := TileRequest{
		LayerID: lyr.ID,
		Tile:    tile,
		SQL:     sql,
		Args:    nil,
	}
	return tr
}

/********************************************************************************/

type queryParameters struct {
	Limit      int
	Properties []string
	Resolution int
	Buffer     int
	Filter     string
	FilterCrs  int
}

// getRequestIntParameter ignores missing parameters and non-integer parameters,
// returning the "unknown integer" value for this case, which is -1
func getQueryIntParameter(q url.Values, param string) int {
	ok := false
	sParam := make([]string, 0)

	for k, v := range q {
		if strings.EqualFold(k, param) {
			sParam = v
			ok = true
			break
		}
	}
	if ok {
		iParam, err := strconv.Atoi(sParam[0])
		if err == nil {
			return iParam
		}
	}
	return -1
}

func getQueryStringParameter(q url.Values, param string) string {
	vals := q[param]
	if vals != nil {
		return vals[0]
	}
	return ""
}

// getRequestPropertiesParameter compares the properties in the request
// with the properties in the table layer, and returns a slice of
// just those that occur in both, or a slice of all table properties
// if there is not query parameter, or no matches
func (lyr *LayerTable) getQueryPropertiesParameter(q url.Values) []string {
	sAtts := make([]string, 0)
	haveProperties := false

	for k, v := range q {
		if strings.EqualFold(k, "properties") {
			sAtts = v
			haveProperties = true
			break
		}
	}

	lyrAtts := (*lyr).Properties
	queryAtts := make([]string, 0, len(lyrAtts))
	haveIDColumn := false

	if haveProperties {
		aAtts := strings.Split(sAtts[0], ",")
		for _, att := range aAtts {
			decAtt, err := url.QueryUnescape(att)
			if err == nil {
				decAtt = strings.Trim(decAtt, " ")
				att, ok := lyrAtts[decAtt]
				if ok {
					if att.Name == lyr.IDColumn {
						haveIDColumn = true
					}
					queryAtts = append(queryAtts, att.Name)
				}
			}
		}
	}
	// No request parameter or no matches, so we want to
	// return all the properties in the table layer
	if len(queryAtts) == 0 {
		for _, v := range lyrAtts {
			queryAtts = append(queryAtts, v.Name)
		}
	}
	if (!haveIDColumn) && lyr.IDColumn != "" {
		queryAtts = append(queryAtts, lyr.IDColumn)
	}
	return queryAtts
}

// getRequestParameters reads user-settables parameters
// from the request URL, or uses the system defaults
// if the parameters are not set
func (lyr *LayerTable) getQueryParameters(q url.Values) queryParameters {
	rp := queryParameters{
		Limit:      getQueryIntParameter(q, "limit"),
		Resolution: getQueryIntParameter(q, "resolution"),
		Buffer:     getQueryIntParameter(q, "buffer"),
		Properties: lyr.getQueryPropertiesParameter(q),
		Filter:     getQueryStringParameter(q, "filter"),
		FilterCrs:  getQueryIntParameter(q, "filter-crs"),
	}
	if rp.Limit < 0 {
		rp.Limit = viper.GetInt("MaxFeaturesPerTile")
	}
	if rp.Resolution < 0 {
		rp.Resolution = viper.GetInt("DefaultResolution")
	}
	if rp.Buffer < 0 {
		rp.Buffer = viper.GetInt("DefaultBuffer")
	}
	if rp.FilterCrs < 0 {
		rp.FilterCrs = 4326
	}
	return rp
}

/********************************************************************************/

func (lyr *LayerTable) getTableDetailJSON(req *http.Request) (TableDetailJSON, error) {
	td := TableDetailJSON{
		ID:           lyr.ID,
		Schema:       lyr.Schema,
		Name:         lyr.Table,
		Description:  lyr.Description,
		GeometryType: lyr.GeometryType,
		MinZoom:      viper.GetInt("DefaultMinZoom"),
		MaxZoom:      viper.GetInt("DefaultMaxZoom"),
	}
	// TileURL is relative to server base
	td.TileURL = fmt.Sprintf("%s/%s/{z}/{x}/{y}.png", serverURLBase(req), url.PathEscape(lyr.ID))

	// Want to add the properties to the Json representation
	// in table order, which is fiddly
	tmpMap := make(map[int]TableProperty)
	tmpKeys := make([]int, 0, len(lyr.Properties))
	for _, v := range lyr.Properties {
		tmpMap[v.order] = v
		tmpKeys = append(tmpKeys, v.order)
	}
	sort.Ints(tmpKeys)
	for _, v := range tmpKeys {
		td.Properties = append(td.Properties, tmpMap[v])
	}

	// Read table bounds and convert to Json
	// which prefers an array form
	bnds, err := lyr.GetBounds()
	if err != nil {
		return td, err
	}
	td.Bounds[0] = bnds.Xmin
	td.Bounds[1] = bnds.Ymin
	td.Bounds[2] = bnds.Xmax
	td.Bounds[3] = bnds.Ymax
	td.Center[0] = (bnds.Xmin + bnds.Xmax) / 2.0
	td.Center[1] = (bnds.Ymin + bnds.Ymax) / 2.0
	return td, nil
}

// https://gist.github.com/kubaszostak/c22a5bb10fe42cbf067c795d9729864e
// GetBoundsExact returns the data coverage extent for a table layer
// in EPSG:4326, clipped to (+/-180, +/-90)
func (lyr *LayerTable) GetBoundsExact() (Bounds, error) {
	bounds := Bounds{}
	extentSQL := fmt.Sprintf(`
	WITH ext AS (
		SELECT
			ST_Transform(
				COALESCE(
					ST_SetSRID(ST_Envelope(ST_Extent(ST_Envelope("%s"))), %d),
					ST_MakeEnvelope(-180, -90, 180, 90, 4326)
				),
			4326) AS geom
		FROM "%s"."%s"
	)
	SELECT
		ST_XMin(ext.geom) AS xmin,
		ST_YMin(ext.geom) AS ymin,
		ST_XMax(ext.geom) AS xmax,
		ST_YMax(ext.geom) AS ymax
	FROM ext
	`, lyr.GeometryColumn, lyr.Srid, lyr.Schema, lyr.Table)

	db, err := dbConnect()
	if err != nil {
		return bounds, err
	}
	var (
		xmin pgtype.Float8
		xmax pgtype.Float8
		ymin pgtype.Float8
		ymax pgtype.Float8
	)
	err = db.QueryRow(context.Background(), extentSQL).Scan(&xmin, &ymin, &xmax, &ymax)
	if err != nil {
		return bounds, tileAppError{
			SrcErr:  err,
			Message: "Unable to calculate table bounds",
		}
	}

	bounds.SRID = 4326
	bounds.Xmin = xmin.Float
	bounds.Ymin = ymin.Float
	bounds.Xmax = xmax.Float
	bounds.Ymax = ymax.Float
	bounds.sanitize()
	return bounds, nil
}

// GetBounds returns the estimated extent for a table layer, transformed to EPSG:4326
func (lyr *LayerTable) GetBounds() (Bounds, error) {
	bounds := Bounds{}
	extentSQL := fmt.Sprintf(`
		WITH ext AS (
			SELECT ST_Transform(ST_SetSRID(extent, %d), 4326) AS geom
			FROM public.raster_columns
			WHERE r_table_schema = '%s'
			  AND r_table_name = '%s'
			  AND r_raster_column = '%s'
		)
		SELECT
			ST_XMin(ext.geom) AS xmin,
			ST_YMin(ext.geom) AS ymin,
			ST_XMax(ext.geom) AS xmax,
			ST_YMax(ext.geom) AS ymax
		FROM ext
		`, lyr.Srid, lyr.Schema, lyr.Table, lyr.GeometryColumn)

	db, err := dbConnect()
	if err != nil {
		return bounds, err
	}

	var (
		xmin pgtype.Float8
		xmax pgtype.Float8
		ymin pgtype.Float8
		ymax pgtype.Float8
	)
	err = db.QueryRow(context.Background(), extentSQL).Scan(&xmin, &ymin, &xmax, &ymax)
	if err != nil {
		return bounds, tileAppError{
			SrcErr:  err,
			Message: "Unable to calculate table bounds",
		}
	}

	// Failed to get estimate? Get the exact bounds.
	if xmin.Status == pgtype.Null {
		warning := fmt.Sprintf("Estimated extent query failed, run 'ANALYZE %s.%s'", lyr.Schema, lyr.Table)
		log.WithFields(log.Fields{
			"event": "request",
			"topic": "detail",
			"key":   warning,
		}).Warn(warning)
		return lyr.GetBoundsExact()
	}

	bounds.SRID = 4326
	bounds.Xmin = xmin.Float
	bounds.Ymin = ymin.Float
	bounds.Xmax = xmax.Float
	bounds.Ymax = ymax.Float
	bounds.sanitize()
	return bounds, nil
}

func chooseOverviewFactor(zoom int) int {
	switch {
		case zoom <= 7:
			return 16
		case zoom <= 11:
			return 8
		case zoom <= 13:
			return 4
		case zoom <= 15:
			return 2
		default:
			return 1 // base table
	}
}

func (lyr *LayerTable) requestSQL(tile *Tile, qp *queryParameters) (string, error) {

	type sqlParameters struct {
		TileZoom       int
		TileX         int
		TileY         int
		TileSQL        string
		QuerySQL       string
		FilterSQL      string
		TileSrid       int
		Resolution     int
		Buffer         int
		// Properties     string
		// MvtParams      string
		Limit          string
		Schema         string
		Table          string
		GeometryColumn string
		Srid           int
	}

	// need both the exact tile boundary for clipping and an
	// expanded version for querying
	tileBounds := tile.Bounds
	queryBounds := tile.Bounds
	// queryBounds.Expand(tile.width() * float64(qp.Buffer) / float64(qp.Resolution))
	tileSQL := tileBounds.SQL()
	tileQuerySQL := queryBounds.SQL()

	filterSQL, err := lyr.filterSQL(qp)
	if err != nil {
		return "", err
	}

	// SRID of the tile we are going to generate, which might be different
	// from the layer SRID in the database
	tileSrid := tile.Bounds.SRID

	// // preserve case and special characters in column names
	// // of SQL query by double quoting names
	// attrNames := make([]string, 0, len(qp.Properties))
	// for _, a := range qp.Properties {
	// 	attrNames = append(attrNames, fmt.Sprintf("\"%s\"", a))
	// }

	// // only specify MVT format parameters we have configured
	// mvtParams := make([]string, 0)
	// mvtParams = append(mvtParams, fmt.Sprintf("'%s', %d", lyr.ID, qp.Resolution))
	// if lyr.GeometryColumn != "" {
	// 	mvtParams = append(mvtParams, fmt.Sprintf("'%s'", lyr.GeometryColumn))
	// }
	// // The idColumn parameter is PostGIS3+ only
	// if globalPostGISVersion >= 3000000 && lyr.IDColumn != "" {
	// 	mvtParams = append(mvtParams, fmt.Sprintf("'%s'", lyr.IDColumn))
	// }

	// Default zoom level is the base table
	table := lyr.Table
	schema := lyr.Schema
	geometryColumn := lyr.GeometryColumn

	// Check if we have any raster overviews that better match the zoom level
	targetFactor := chooseOverviewFactor(tile.Zoom)
	for _, overview := range lyr.RasterOverviews {
		if overview.OverviewFactor < targetFactor {
			// We found a matching overview, so we can use it
			table = overview.TableName
			schema = overview.TableSchema
			geometryColumn = overview.RasterColumn
			fmt.Printf("Using raster overview %s.%s.%s with factor %d(target %d) for zoom %d\n",
				schema, table, geometryColumn, overview.OverviewFactor, targetFactor, tile.Zoom)
			break
		}
	}

	sp := sqlParameters{
		TileZoom:       tile.Zoom,
		TileX:         tile.X,
		TileY:         tile.Y,
		TileSQL:        tileSQL,
		QuerySQL:       tileQuerySQL,
		FilterSQL:      filterSQL,
		TileSrid:       tileSrid,
		Resolution:     qp.Resolution,
		Buffer:         qp.Buffer,
		// Properties:     strings.Join(attrNames, ", "),
		// MvtParams:      strings.Join(mvtParams, ", "),
		Schema:         schema,
		Table:          table,
		GeometryColumn: geometryColumn,
		Srid:           lyr.Srid,
	}

	if qp.Limit > 0 {
		sp.Limit = fmt.Sprintf("LIMIT %d", qp.Limit)
	}

	// https://postgis.net/docs/RT_ST_ColorMap.html
	// Alignment-safe raster tile SQL: ensures proper raster alignment using a base raster from tile envelope and clipping source rasters.
	tmplSQL := `
	WITH
	  bounds AS (
	    SELECT ST_TileEnvelope({{ .TileZoom }}, {{ .TileX }}, {{ .TileY }}) AS env
	  ),
	  base_tile AS (
	  	SELECT ST_AddBand(
		ST_MakeEmptyRaster(
			{{ .Resolution }}, {{ .Resolution }},
			ST_XMin(env), ST_YMax(env),
			(ST_XMax(env) - ST_XMin(env)) / ({{ .Resolution }}),
			(ST_YMax(env) - ST_YMin(env)) / -({{ .Resolution }}),
			0, 0, {{ .Srid }}
		),
		'8BUI'::text, 0, NULL
		) AS rast
	    FROM bounds
	  ),
	  base_ext AS (
	  	SELECT ST_SetSrid(ST_Extent(ST_Envelope(base_tile.rast)), {{ .Srid }}) AS env FROM base_tile
	  ),
	  clipped AS (
	    SELECT
			ST_Clip(t."{{ .GeometryColumn }}", ST_Buffer(bounds.env, 2*(ST_XMax(env) - ST_XMin(env)) / ({{ .Resolution }})), touched => true)
			--ST_Clip(t."{{ .GeometryColumn }}", bounds.env, touched => true)
			-- t."{{ .GeometryColumn }}"
			AS rast
	    FROM "{{ .Schema }}"."{{ .Table }}" t, base_ext bounds
	    WHERE ST_Intersects(t."{{ .GeometryColumn }}", bounds.env)
	    {{ .FilterSQL }}
	    {{ .Limit }}
	  ),
	  padded AS (
	    SELECT ST_Union(rast) AS rast FROM (
			SELECT rast FROM base_tile
			UNION ALL
			SELECT ST_Resample(clipped.rast, base_tile.rast) as rast FROM clipped, base_tile
		)
	  )
	SELECT ST_AsPNG(
		-- rast
		ST_Clip(rast, bounds.env, touched => true)
		-- ST_ColorMap(rast, 1, 'bluered')
	) FROM padded, bounds;
	`


	// tmplSQL := `
	// WITH
	// bounds AS (
	// 	SELECT
	// 		ST_Transform(ST_TileEnvelope({{ .TileZoom }}, {{ .TileX }}, {{ .TileY }}), {{ .Srid }}) as env,
	// 		ST_Transform(ST_TileEnvelope({{ .TileZoom }}, {{ .TileX }}, {{ .TileY }}), {{ .Srid }}) AS geom_clip
	// )
	// ,rasts AS (
	// 	SELECT ST_AddBand(
	// 	ST_MakeEmptyRaster(
	// 		256, 256,
	// 		ST_XMin(env), ST_YMax(env),
	// 		(ST_XMax(env) - ST_XMin(env)) / 256,
	// 		(ST_YMax(env) - ST_YMin(env)) / -256,
	// 		0, 0, {{ .Srid }}
	// 	),
	// 	'8BUI'::text, 0, NULL
	// 	) AS "{{ .GeometryColumn }}"
	// 	FROM bounds
	// 	UNION ALL
	// 	SELECT ST_Clip(
	// 		t."{{ .GeometryColumn }}",
	// 		bounds.geom_clip,
	// 		touched => true
	// 	) as "{{ .GeometryColumn }}"
	// 	FROM "{{ .Schema }}"."{{ .Table }}" t, bounds
	// 	WHERE
	// 		ST_Intersects(t."{{ .GeometryColumn }}", bounds.geom_clip)
	// 		{{ .FilterSQL }}
	// 	{{ .Limit }}
	// )
	// -- ,ref_tile AS (
    // -- SELECT ST_AddBand(
	// -- 	ST_MakeEmptyRaster(
	// -- 		256, 256,
	// -- 		ST_XMin(env), ST_YMax(env),
	// -- 		(ST_XMax(env) - ST_XMin(env)) / 256,
	// -- 		(ST_YMax(env) - ST_YMin(env)) / -256,
	// -- 		0, 0, {{ .Srid }}
	// -- 	),
	// -- 	'8BUI'::text, 0, NULL
	// -- 	) AS rast
	// -- 	FROM bounds
	// -- )
	// SELECT
	// 	ST_AsPNG(
	// 		ST_ColorMap(
	// 		-- ST_Resample(
	// 			ST_Union(rasts."{{ .GeometryColumn }}")
	// 		-- , ref_tile.rast)
	// 		, 1, 'bluered')
	// 	) AS png
	// FROM rasts
	// -- ,ref_tile
	// -- group by ref_tile.rast
	// `
	// tmplSQL := `
	// WITH
	// bounds AS (
	// 	SELECT ST_SetSRID(ST_TileEnvelope({{ .TileZoom }}, {{ .TileX }}, {{ .TileY }}), {{ .TileSrid }}) as env
	// )
	// ,rasts AS (
	// 	SELECT t."{{ .GeometryColumn }}" as "{{ .GeometryColumn }}"
	// 	FROM "{{ .Schema }}"."{{ .Table }}" t, bounds
	// 	WHERE
	// 		ST_Intersects(t."{{ .GeometryColumn }}", ST_Transform(bounds.env, {{ .Srid }}))
	// 		{{ .FilterSQL }}
	// 	{{ .Limit }}
	// )
	// ,rast AS (
	// 	SELECT ST_Clip(
	// 		t."{{ .GeometryColumn }}",
	// 		ST_Transform(bounds.env, {{ .Srid }})
	// 	) AS "{{ .GeometryColumn }}"
	// 	FROM rasts t, bounds
	// )
	// ,ref_tile AS (
    // SELECT ST_AddBand(
	// 	ST_MakeEmptyRaster(
	// 		256, 256,
	// 		ST_XMin(env), ST_YMax(env),
	// 		(ST_XMax(env) - ST_XMin(env)) / 256,
	// 		(ST_YMax(env) - ST_YMin(env)) / -256,
	// 		0, 0, {{ .TileSrid }}
	// 	),
	// 	'8BUI'::text, 0, NULL
	// 	) AS rast
	// 	FROM bounds
	// )
	// SELECT
	// 	ST_AsPNG(ST_ColorMap(
	// 		ST_Resample(
	// 			ST_Transform(ST_Union(rast."{{ .GeometryColumn }}"), {{ .TileSrid }})
	// 			, ref_tile.rast)
	// 			-- , 256, 256)
	// 		, 1, 'bluered'
	// 	)) AS png
	// FROM rast
	// , ref_tile
	// group by ref_tile.rast
	// `

	// tmplSQL := `
	// WITH
	// bounds AS (
	// 	SELECT ST_TileEnvelope({{ .TileZoom }}, {{ .TileX }}, {{ .TileY }}) as env
	// 	-- ST_SetSRID(, {{ .TileSrid }}) AS env
	// )
	// ,rasts AS (
	// 	SELECT t."{{ .GeometryColumn }}" as "{{ .GeometryColumn }}"
	// 	FROM "{{ .Schema }}"."{{ .Table }}" t, bounds
	// 	WHERE
	// 		ST_Intersects(t."{{ .GeometryColumn }}", ST_Transform(bounds.env, {{ .Srid }}))
	// 		{{ .FilterSQL }}
	// 	{{ .Limit }}
	// )
	// ,rast AS (
	// 	SELECT ST_Clip(
	// 		t."{{ .GeometryColumn }}",
	// 		ST_Transform(bounds.env, {{ .Srid }}),
	// 		false
	// 	) AS "{{ .GeometryColumn }}"
	// 	FROM rasts t, bounds
	// )
	// -- ,ref_tile AS (
    // -- SELECT ST_AddBand(
	// -- 	ST_MakeEmptyRaster(
	// -- 		256, 256,
	// -- 		ST_XMin(env), ST_YMax(env),
	// -- 		(ST_XMax(env) - ST_XMin(env)) / 256,
	// -- 		(ST_YMax(env) - ST_YMin(env)) / -256,
	// -- 		0, 0, {{ .TileSrid }}
	// -- 	),
	// -- 	'8BUI'::text, 0, NULL
	// -- 	) AS rast
	// -- 	FROM bounds
	// -- )
	// SELECT
	// 	ST_AsPNG(ST_ColorMap(
	// 		-- ST_Resample(
	// 			ST_Transform(ST_Union(rast."{{ .GeometryColumn }}"), {{ .TileSrid }})
	// 			-- , ref_tile.rast)
	// 			-- , 256, 256)
	// 		, 1, 'bluered'
	// 	)) AS png
	// FROM rast
	// -- , ref_tile
	// -- group by ref_tile.rast
	// `
	// tmplSQL := `
	// 	WITH bounds AS (
	// 	SELECT ST_Transform(ST_TileEnvelope({{ .TileZoom }}, {{ .TileX }}, {{ .TileY }}), {{ .Srid }}) AS geom
	// 	)
	// 	SELECT ST_AsPNG(ST_ColorMap(
	// 		ST_Clip(
	// 			ST_SnapToGrid(ST_Union(r."{{ .GeometryColumn }}"), 0.1, 0.1, -180, 90),
	// 			b.geom,
	// 			true
	// 		), 1, 'bluered')
	// 	)
	// 	FROM "{{ .Schema }}"."{{ .Table }}" r, bounds b
	// 	WHERE ST_Intersects(r."{{ .GeometryColumn }}", b.geom)
	// 	GROUP BY b.geom;
	// `
	// SELECT
	// 	ST_AsPNG(ST_ColorMap(
	// 		ST_Resample(
	// 		ST_Union(rast."{{ .GeometryColumn }}"),
	// 		ST_AddBand(ST_MakeEmptyRaster(256, 256,
	// 			ST_XMin(rast.geom_envelope), ST_YMax(rast.geom_envelope),
	// 			(ST_XMax(rast.geom_envelope) - ST_XMin(rast.geom_envelope)) / 256,
	// 			(ST_YMax(rast.geom_envelope) - ST_YMin(rast.geom_envelope)) / -256,
	// 			0, 0, {{ .Srid }}
	// 		), '8BUI'::text, 0, NULL))
	// 	, 1, 'bluered'))
	// FROM (
	// 	SELECT ST_Clip(
	// 		t."{{ .GeometryColumn }}",
	// 		bounds.geom_envelope
	// 	  ) AS "{{ .GeometryColumn }}",
	// 	  bounds.geom_envelope
	// 	FROM "{{ .Schema }}"."{{ .Table }}" t, (
	// 		SELECT
	// 			ST_Transform(ST_TileEnvelope({{ .TileZoom }}, {{ .TileX }}, {{ .TileY }}), {{ .Srid }}) AS geom_envelope
	// 		) bounds
	// 	WHERE
	// 		ST_Intersects(t."{{ .GeometryColumn }}", bounds.geom_envelope)
	// 		{{ .FilterSQL }}
	// 	{{ .Limit }}
	// ) rast
	sql, err := renderSQLTemplate("tabletilesql", tmplSQL, sp)
	// fmt.Printf("Table SQL: %s\n", sql)
	if err != nil {
		return "", err
	}
	return sql, err
}

func (lyr *LayerTable) filterSQL(qp *queryParameters) (string, error) {
	//filter := "pop_est < 2000000"
	filter := qp.Filter
	sql, err := cql.TranspileToSQL(filter, qp.FilterCrs, lyr.Srid)
	if err != nil {
		return "", err
	}
	if sql != "" {
		sql = "AND " + sql
	}
	return sql, nil
}

func getTableLayers() ([]LayerTable, error) {

	layerSQL := `
	SELECT
		Format('%s.%s', n.nspname, c.relname) AS id,
		n.nspname AS schema,
		c.relname AS table,
		coalesce(d.description, '') AS description,
		a.attname AS geometry_column,
		COALESCE(rc.srid, 0) AS srid,
		rtrim(postgis_typmod_type(a.atttypmod), 'ZM') AS geometry_type,
		coalesce(case when it.typname is not null then ia.attname else null end, '') AS id_column,
		(
			SELECT json_agg(json_build_object(
				'o_table_schema', ro.o_table_schema,
				'o_table_name', ro.o_table_name,
				'o_raster_column', ro.o_raster_column,
				'overview_factor', ro.overview_factor
			) ORDER BY ro.overview_factor DESC)
			FROM raster_overviews ro
			WHERE ro.r_table_schema = n.nspname
			  AND ro.r_table_name = c.relname
			  AND ro.r_raster_column = a.attname
		) AS raster_overviews,
		(
			SELECT array_agg(ARRAY[sa.attname, st.typname, coalesce(da.description,''), sa.attnum::text]::text[] ORDER BY sa.attnum)
			FROM pg_attribute sa
			JOIN pg_type st ON sa.atttypid = st.oid
			LEFT JOIN pg_description da ON (c.oid = da.objoid and sa.attnum = da.objsubid)
			WHERE sa.attrelid = c.oid
			AND sa.attnum > 0
			AND NOT sa.attisdropped
			AND st.typname NOT IN ('geometry', 'geography')
		) AS props
	FROM pg_class c
	JOIN pg_namespace n ON (c.relnamespace = n.oid)
	JOIN pg_attribute a ON (a.attrelid = c.oid)
	JOIN pg_type t ON (a.atttypid = t.oid)
	LEFT JOIN pg_description d ON (c.oid = d.objoid and d.objsubid = 0)
	LEFT JOIN pg_index i ON (c.oid = i.indrelid AND i.indisprimary AND i.indnatts = 1)
	LEFT JOIN pg_attribute ia ON (ia.attrelid = i.indexrelid)
	LEFT JOIN pg_type it ON (ia.atttypid = it.oid AND it.typname in ('int2', 'int4', 'int8'))
	LEFT JOIN raster_columns rc ON rc.r_table_schema = n.nspname
                               AND rc.r_table_name = c.relname
                               AND rc.r_raster_column = a.attname
	WHERE c.relkind IN ('r', 'v', 'm', 'p', 'f')
		AND t.typname = 'raster'
		AND has_table_privilege(c.oid, 'select')
		AND has_schema_privilege(n.oid, 'usage')
		-- AND postgis_typmod_srid(a.atttypmod) > 0
	ORDER BY 1
	`

	db, connerr := dbConnect()
	if connerr != nil {
		return nil, connerr
	}

	rows, err := db.Query(context.Background(), layerSQL)
	if err != nil {
		return nil, connerr
	}

	// Reset array of layers
	layerTables := make([]LayerTable, 0)
	for rows.Next() {

		var (
			id, schema, table, description, geometryColumn string
			srid                                           int
			geometryType, idColumn                         string
			rasterOverviews                                pgtype.JSON
			atts                                           pgtype.TextArray
		)

		err := rows.Scan(&id, &schema, &table, &description, &geometryColumn,
			&srid, &geometryType, &idColumn, &rasterOverviews, &atts)
		if err != nil {
			return nil, err
		}

		// We use https://godoc.org/github.com/jackc/pgtype#TextArray
		// here to scan the text[][] map of property name/type
		// created in the query. It gets a little ugly demapping the
		// pgx TextArray type, but it is at least native handling of
		// the array. It's complex because of PgSQL ARRAY generality
		// really, no fault of pgx
		properties := make(map[string]TableProperty)

		if atts.Status == pgtype.Present {
			arrLen := atts.Dimensions[0].Length
			arrStart := atts.Dimensions[0].LowerBound - 1
			elmLen := atts.Dimensions[1].Length
			for i := arrStart; i < arrLen; i++ {
				pos := i * elmLen
				elmID := atts.Elements[pos].String
				elm := TableProperty{
					Name:        elmID,
					Type:        atts.Elements[pos+1].String,
					Description: atts.Elements[pos+2].String,
				}
				elm.order, _ = strconv.Atoi(atts.Elements[pos+3].String)
				properties[elmID] = elm
			}
		}

		// Unmarshal rasterOverviews JSON into slice of RasterOverview
		var rasterOverviewArray []RasterOverview
		if rasterOverviews.Status == pgtype.Present && len(rasterOverviews.Bytes) > 0 {
			err := json.Unmarshal(rasterOverviews.Bytes, &rasterOverviewArray)
			if err != nil {
				return nil, fmt.Errorf("unable to parse raster overview metadata: %w", err)
			}
		}

		// "schema.tablename" is our unique key for table layers
		lyr := LayerTable{
			ID:              id,
			Schema:          schema,
			Table:           table,
			Description:     description,
			GeometryColumn:  geometryColumn,
			Srid:            srid,
			GeometryType:    geometryType,
			IDColumn:        idColumn,
			RasterOverviews: rasterOverviewArray,
			Properties:      properties,
		}

		layerTables = append(layerTables, lyr)
	}
	// Check for errors from iterating over rows.
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return layerTables, nil
}
