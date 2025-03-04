package operator

import (
	"crypto/rsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"time"

	"github.com/bloxapp/ssv-dkg/pkgs/wire"
	ssvspec_types "github.com/bloxapp/ssv-spec/types"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/httprate"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

type Server struct {
	Logger     *zap.Logger
	HttpServer *http.Server
	Router     chi.Router
	State      *Switch
}

type KeySign struct {
	ValidatorPK ssvspec_types.ValidatorPK
	SigningRoot []byte
}

// Encode returns a msg encoded bytes or error
func (msg *KeySign) Encode() ([]byte, error) {
	return json.Marshal(msg)
}

// Decode returns error if decoding failed
func (msg *KeySign) Decode(data []byte) error {
	return json.Unmarshal(data, msg)
}

// TODO: either do all json or all SSZ
const ErrTooManyOperatorRequests = `{"error": "too many requests to operator"}`
const ErrTooManyDKGRequests = `{"error": "too many requests to initiate DKG"}`

func RegisterRoutes(s *Server) {
	// Add general rate limiter
	s.Router.Use(httprate.Limit(
		500,
		1*time.Minute,
		httprate.WithLimitHandler(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(ErrTooManyOperatorRequests))
		}),
	))
	s.Router.Route("/init", func(r chi.Router) {
		r.Use(httprate.Limit(
			5,
			time.Minute,
			httprate.WithLimitHandler(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(ErrTooManyDKGRequests))
			}),
		))
		r.Post("/", func(writer http.ResponseWriter, request *http.Request) {
			s.Logger.Debug("incoming INIT msg")
			rawdata, _ := io.ReadAll(request.Body)
			signedInitMsg := &wire.SignedTransport{}
			if err := signedInitMsg.UnmarshalSSZ(rawdata); err != nil {
				s.Logger.Error("parsing failed: ", zap.Error(err))
				writer.WriteHeader(http.StatusBadRequest)
				writer.Write(wire.MakeErr(err))
				return
			}

			// Validate that incoming message is an init message
			if signedInitMsg.Message.Type != wire.InitMessageType {
				s.Logger.Error("received bad msg non init message sent to init route")
				writer.WriteHeader(http.StatusBadRequest)
				writer.Write(wire.MakeErr(errors.New("not init message to init route")))
				return
			}
			reqid := signedInitMsg.Message.Identifier
			logger := s.Logger.With(zap.String("reqid", hex.EncodeToString(reqid[:])))
			logger.Debug("initiating instance with init data")
			b, err := s.State.InitInstance(reqid, signedInitMsg.Message, signedInitMsg.Signature)
			if err != nil {
				logger.Error(fmt.Sprintf("failed to initiate instance err:%v", err))

				writer.WriteHeader(http.StatusBadRequest)
				writer.Write(wire.MakeErr(err))
				return
			}
			logger.Info("✅ Instance started successfully")

			writer.WriteHeader(http.StatusOK)
			writer.Write(b)
		})
	})
	s.Router.Route("/dkg", func(r chi.Router) {
		r.Post("/", func(writer http.ResponseWriter, request *http.Request) {
			s.Logger.Debug("received a dkg protocol message")
			rawdata, err := io.ReadAll(request.Body)
			if err != nil {
				writer.WriteHeader(http.StatusBadRequest)
				writer.Write(wire.MakeErr(err))
				return
			}
			b, err := s.State.ProcessMessage(rawdata)
			if err != nil {
				writer.WriteHeader(http.StatusBadRequest)
				writer.Write(wire.MakeErr(err))
				return
			}
			writer.WriteHeader(http.StatusOK)
			writer.Write(b)
		})
	})
}

func New(key *rsa.PrivateKey, logger *zap.Logger) *Server {
	r := chi.NewRouter()
	swtch := NewSwitch(key, logger)
	s := &Server{
		Logger: logger,
		Router: r,
		State:  swtch,
	}
	RegisterRoutes(s)
	return s
}

func (s *Server) Start(port uint16) error {
	srv := &http.Server{Addr: fmt.Sprintf(":%v", port), Handler: s.Router}
	s.HttpServer = srv
	err := s.HttpServer.ListenAndServe()
	if err != nil {
		return err
	}
	s.Logger.Info("✅ Server is listening for incoming requests", zap.Uint16("port", port))
	return nil
}

func (s *Server) Stop() error {
	return s.HttpServer.Close()
}
