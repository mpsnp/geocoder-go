package api

import (
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/GameTec-live/geocoder-go/geocoder"
	"github.com/gin-gonic/gin"
)

func Router(service *geocoder.Service) *gin.Engine {
	router := gin.New()
	_ = router.SetTrustedProxies(nil)
	router.Use(gin.Logger(), gin.Recovery(), securityHeaders)

	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	router.GET("/readyz", func(c *gin.Context) {
		packs := service.PackNames()
		c.JSON(http.StatusOK, gin.H{"status": "ready", "pack_count": len(packs), "packs": packs})
	})

	router.GET("/geocode", func(c *gin.Context) {
		query := strings.TrimSpace(c.Query("q"))
		if query == "" {
			problem(c, http.StatusBadRequest, "missing_query", "q is required")
			return
		}
		if utf8.RuneCountInString(query) > 512 {
			problem(c, http.StatusBadRequest, "query_too_long", "q must not exceed 512 characters")
			return
		}
		limit, ok := intParameter(c, "limit", 10)
		if !ok {
			return
		}
		lat, lon, ok := optionalPoint(c)
		if !ok {
			return
		}
		results, err := service.Geocode(c.Request.Context(), geocoder.SearchOptions{
			Query: query, CountryCode: c.Query("country"), Latitude: lat, Longitude: lon, Limit: limit,
		})
		if err != nil {
			problem(c, http.StatusInternalServerError, "search_failed", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"query": query, "count": len(results), "results": results})
	})

	router.GET("/reverse", func(c *gin.Context) {
		lat, ok := requiredFloat(c, "lat")
		if !ok {
			return
		}
		lon, ok := requiredFloat(c, "lon")
		if !ok {
			return
		}
		radius, ok := floatParameter(c, "radius_m", 5000)
		if !ok {
			return
		}
		limit, ok := intParameter(c, "limit", 1)
		if !ok {
			return
		}
		results, err := service.Reverse(c.Request.Context(), geocoder.ReverseOptions{
			Latitude: lat, Longitude: lon, RadiusMeter: radius, Limit: limit,
		})
		if err != nil {
			problem(c, http.StatusBadRequest, "reverse_failed", err.Error())
			return
		}
		c.JSON(http.StatusOK, gin.H{"count": len(results), "results": results})
	})

	return router
}

func securityHeaders(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Next()
}

func optionalPoint(c *gin.Context) (*float64, *float64, bool) {
	latText, lonText := strings.TrimSpace(c.Query("lat")), strings.TrimSpace(c.Query("lon"))
	if latText == "" && lonText == "" {
		return nil, nil, true
	}
	if latText == "" || lonText == "" {
		problem(c, http.StatusBadRequest, "invalid_location_bias", "lat and lon must be supplied together")
		return nil, nil, false
	}
	lat, err := strconv.ParseFloat(latText, 64)
	if err != nil || lat < -90 || lat > 90 {
		problem(c, http.StatusBadRequest, "invalid_lat", "lat must be a number between -90 and 90")
		return nil, nil, false
	}
	lon, err := strconv.ParseFloat(lonText, 64)
	if err != nil || lon < -180 || lon > 180 {
		problem(c, http.StatusBadRequest, "invalid_lon", "lon must be a number between -180 and 180")
		return nil, nil, false
	}
	return &lat, &lon, true
}

func requiredFloat(c *gin.Context, name string) (float64, bool) {
	text := strings.TrimSpace(c.Query(name))
	if text == "" {
		problem(c, http.StatusBadRequest, "missing_"+name, name+" is required")
		return 0, false
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		problem(c, http.StatusBadRequest, "invalid_"+name, name+" must be a number")
		return 0, false
	}
	return value, true
}

func floatParameter(c *gin.Context, name string, defaultValue float64) (float64, bool) {
	text := strings.TrimSpace(c.Query(name))
	if text == "" {
		return defaultValue, true
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		problem(c, http.StatusBadRequest, "invalid_"+name, name+" must be a number")
		return 0, false
	}
	return value, true
}

func intParameter(c *gin.Context, name string, defaultValue int) (int, bool) {
	text := strings.TrimSpace(c.Query(name))
	if text == "" {
		return defaultValue, true
	}
	value, err := strconv.Atoi(text)
	if err != nil {
		problem(c, http.StatusBadRequest, "invalid_"+name, name+" must be an integer")
		return 0, false
	}
	return value, true
}

func problem(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}
