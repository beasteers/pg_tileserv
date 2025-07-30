# Shortcuts to load all data in PostGIS DB in Docker Container
# NB the docker-compose stack must be running !
#

# Create overviews for the raster data
docker-compose exec pg_rasterserv_db sh -c \
  "psql -U tileserv -d tileserv \
  -c \"CREATE EXTENSION postgis_raster;ALTER SYSTEM SET postgis.gdal_enabled_drivers TO 'ENABLE_ALL';SELECT pg_reload_conf();\""

# # Load Admin 0 countries
# docker-compose exec pg_tileserv_db sh -c "shp2pgsql -D -s 4326 /work/ne_50m_admin_0_countries.shp | psql -U tileserv -d tileserv"

# # Load Vancouver Water Hydrants
# docker-compose exec pg_tileserv_db sh -c "shp2pgsql -D -s 26910 -I /work/water-hydrants.shp hydrants | psql -U tileserv -d tileserv"

# # Load SQL Functions for OpenLayers example
# cp ../openlayers/openlayers-function-click.sql ./data/
# docker-compose exec pg_tileserv_db sh -c "cat /work/openlayers-function-click.sql | psql -U tileserv -d tileserv"
# rm ./data/openlayers-function-click.sql

wget https://download.osgeo.org/geotiff/samples/GeogToWGS84GeoKey/GeogToWGS84GeoKey5.tif -O ./data/GeogToWGS84GeoKey5.tif
wget https://github.com/GeoTIFF/georaster-layer-for-leaflet/files/5438151/nt_20201024_f18_nrt_s.zip -O ./data/nt_20201024_f18_nrt_s.zip

wget https://pmmpublisher.pps.eosdis.nasa.gov/products/s3/r01/gpm_1d/2025/209/gpm_1d.20250714.tif -O ./data/gpm_1d.20250714.tif

# # Unzip the downloaded file
# unzip -o ./data/nt_20201024_f18_nrt_s.zip -d ./data

# gdal_translate \
#   -ot Byte \
#   -scale 0 100 0 255 \
#   -of GTiff \
#   data/nt_20201024_f18_nrt_s.tif \
#   data/nt_20201024_f18_nrt_s_8.tif
gdalwarp \
  -t_srs EPSG:3857 \
  -r bilinear \
  -tr 1000 1000 \
  -co COMPRESS=DEFLATE \
  gpm_1d.20250714.tif gpm_1d.20250714_3857.tif

# "https://s3.eu-central-1.wasabisys.com/openlandmap/predicted1km/pnv_fapar_proba.v.annual_d_1km_s0..0cm_2014..2017_v0.1.tif"

# enable PostGIS raster extension
docker-compose exec pg_rasterserv_db \
  psql -U tileserv -d tileserv \
  -c "CREATE EXTENSION IF NOT EXISTS postgis_raster;"

# Load sample raster data (GeoTIFF)
docker-compose exec pg_rasterserv_db sh -c \
  "raster2pgsql -s 4326 -C -I /work/GeogToWGS84GeoKey5.tif geogkey5 \
    | psql -U tileserv -d tileserv"

# Load sample raster data (GeoTIFF)
docker-compose exec pg_rasterserv_db sh -c \
    "raster2pgsql -s 3031 -C -I /work/nt_20201024_f18_nrt_s_8.tif sea_ice \
    | psql -U tileserv -d tileserv"

docker-compose exec pg_rasterserv_db sh -c \
    "raster2pgsql -s 3857 -C -I -P -t 512x512 work/gpm_1d.20250714_3857.tif precip \
    | psql -U tileserv -d tileserv"

# Create overviews for the raster data
docker-compose exec pg_rasterserv_db sh -c \
  "psql -U tileserv -d tileserv \
  -c \"SELECT ST_CreateOverview('public.precip'::regclass, 'rast', 2);\""
docker-compose exec pg_rasterserv_db sh -c \
  "psql -U tileserv -d tileserv \
  -c \"SELECT ST_CreateOverview('public.precip'::regclass, 'rast', 4);\""
docker-compose exec pg_rasterserv_db sh -c \
  "psql -U tileserv -d tileserv \
  -c \"SELECT ST_CreateOverview('public.precip'::regclass, 'rast', 8);\""
docker-compose exec pg_rasterserv_db sh -c \
  "psql -U tileserv -d tileserv \
  -c \"SELECT ST_CreateOverview('public.precip'::regclass, 'rast', 16);\""


# CREATE OR REPLACE FUNCTION public.checkerboard(z integer, x integer, y integer)
# RETURNS bytea AS $$
# DECLARE
#     rast raster;
#     env geometry;
# BEGIN
#     -- Generate the tile envelope in Web Mercator
#     env := ST_TileEnvelope(z, x, y);

#     -- Create a dummy raster that fills the tile
#     rast := ST_AddBand(
#         ST_MakeEmptyRaster(
#             256, 256,
#             ST_XMin(env), ST_YMax(env),
#             (ST_XMax(env) - ST_XMin(env)) / 256,
#             (ST_YMax(env) - ST_YMin(env)) / -256,
#             0, 0, 3857
#         ),
#         '8BUI'::text, (
#             CASE WHEN (MOD(x, 2) = 1) <>  (MOD(y, 2) = 1) THEN 255
#                  ELSE (x + y) * power(2, 8 - 1 - z)
#             END
#         ), nodataval => 255
#     );

#     RETURN ST_AsPNG(rast);
# END;
# $$ LANGUAGE plpgsql STABLE;
