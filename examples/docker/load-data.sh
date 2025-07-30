# Shortcuts to load all data in PostGIS DB in Docker Container
# NB the docker-compose stack must be running !
#

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
# Unzip the downloaded file
unzip -o ./data/nt_20201024_f18_nrt_s.zip -d ./data

gdal_translate \
  -ot Byte \
  -scale 0 100 0 255 \
  -of GTiff \
  data/nt_20201024_f18_nrt_s.tif \
  data/nt_20201024_f18_nrt_s_8.tif


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
    "raster2pgsql -s 4326 -C -I -F -t "auto" work/gpm_1d.20250726.tif precip \
    | psql -U tileserv -d tileserv"
