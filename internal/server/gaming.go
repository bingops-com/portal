package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/bingops-com/portal/internal/gaming"
)

func (s *Server) gamesGuard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Games == nil {
			writeError(w, http.StatusNotFound, "gestion des jeux non activée")
			return
		}
		// Unlike layout editing, game commands always require OIDC, even when
		// the portal has been configured without a login for its ordinary views.
		if s.Auth == nil || s.Auth.User(r) == nil {
			writeError(w, http.StatusUnauthorized, "connexion propriétaire requise pour gérer les serveurs")
			return
		}
		next(w, r)
	}
}
func (s *Server) getGames(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	value, err := s.Games.Read(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	observed, liveErr := s.Deps.ObserveGaming(ctx, s.Games.Config)
	out := map[string]any{"desired": value, "observed": observed, "canWrite": value.Writable && !s.ReadOnly}
	if liveErr != nil {
		out["observationError"] = liveErr.Error()
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) changeGame(preview bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var change gaming.Change
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if dec.Decode(&change) != nil || dec.Decode(new(any)) != io.EOF {
			writeError(w, http.StatusBadRequest, "commande de jeu invalide")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		var result gaming.Result
		var err error
		if preview {
			result, err = s.Games.Preview(ctx, r.PathValue("name"), change)
		} else {
			user := s.Auth.User(r)
			actor := user.Email
			if actor == "" {
				actor = user.Name
			}
			result, err = s.Games.Apply(ctx, r.PathValue("name"), actor, change)
		}
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, gaming.ErrConflict) {
				status = http.StatusConflict
			}
			if errors.Is(err, gaming.ErrCredentials) {
				status = http.StatusServiceUnavailable
			}
			writeError(w, status, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}
