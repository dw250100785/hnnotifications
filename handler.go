package main

import (
	"errors"
	"github.com/gorilla/mux"
	"net/http"
	"net/url"
	"strconv"
)

const (
	linkSentMsg     = "An account verification email has been sent."
	subscribedMsg   = "Your account is now active!"
	scoreUpdatedMsg = "Your score threshold has been successfully updated!"
	unsubscribedMsg = "You have been successfully unsubscribed."
)

var (
	errInvalidEmail = errors.New("Error: The email address is not valid!")
	errInvalidScore = errors.New("Error: The score field must be a number!")
	errInvalidLink  = errors.New("Error: The link is not valid.")
	errNotFound     = errors.New("Error: The email address you provided is not subscribed to this service!")
)

// errInternal represents an internal server error.
type errInternal struct{ error }

// errMessage represents a meaningful error message, that will be sent to the user.
type errMessage struct{ error }

// Context carries http session information. It will be passed to all HTTP handlers.
// TODO: Include user information, simplifying authentication management.
type Context struct {
	db *Database
}

// newContext creates a new Context, ready to be passed to a HTTP handler.
func newContext() *Context {
	return &Context{
		db: newDatabase(),
	}
}

// handler wraps a custom handler function returning a standard HandlerFunc closure.
func handler(f func(ctx *Context, w http.ResponseWriter, r *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		Logger.Printf("%s %s %s\n", r.Method, r.URL.Path, r.URL.RawQuery)
		ctx := newContext()
		defer ctx.db.close()

		err := f(ctx, w, r)

		if err == nil {
			return
		}

		// Log the error, and depending on the type, display it to the user.
		Logger.Println(err)
		switch err.(type) {
		case errMessage:
			w.WriteHeader(http.StatusBadRequest)
			writeMessage(err.Error(), w)
		case errInternal:
		default:
			w.WriteHeader(http.StatusInternalServerError)
			writeMessage("Oops! An error ocurred.", w)
		}
	}
}

// setupHandlers registers the HTTP handlers of the app.
func setupHandlers() {
	router := mux.NewRouter()
	router.HandleFunc("/subscribe", handler(SubscribeHandler)).
		Methods("POST")
	router.HandleFunc("/activate", handler(ActivateHandler)).
		Methods("GET")
	router.HandleFunc("/unsubscribe", handler(UnsubscribeHandler)).
		Methods("GET", "POST")

	// serve settings.html static file. index.html works the same way,
	// though it's automatically handled by the root file server handler.
	router.HandleFunc("/settings", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./public/settings.html")
	})

	router.PathPrefix("/").Handler(http.FileServer(http.Dir("./public/")))
	http.Handle("/", router)
}

// SubscribeHandler is the HTTP handler for managing new subscriptions; It handles '/subscribe'.
func SubscribeHandler(ctx *Context, w http.ResponseWriter, r *http.Request) error {
	email, ok := parseEmail(r)
	if !ok {
		return errMessage{errInvalidEmail}
	}
	score, ok := parseScore(r)
	if !ok {
		return errMessage{errInvalidScore}
	}

	q := url.Values{} // Link query parameters.
	u, ok := ctx.db.findUser(email)
	if ok {
		// The user already exists. The score will be added to the query.
		q.Set("score", strconv.Itoa(score))
		u.Token = newToken() // reset user token.
		if err := ctx.db.updateToken(u.Id, u.Token); err != nil {
			return errInternal{err}
		}
	} else {
		u = newUser(email, score)
		if err := ctx.db.upsertUser(u); err != nil {
			return errInternal{err}
		}
	}

	q.Set("email", u.Email)
	q.Set("token", u.Token)
	link := config.Url + "/activate?" + q.Encode()
	go sendVerification(email, link)

	return writeMessage(linkSentMsg, w)
}

// ActivateHandler is the HTTP handler for managing account activations; It handles '/activate'.
func ActivateHandler(ctx *Context, w http.ResponseWriter, r *http.Request) error {
	email, token := r.FormValue("email"), r.FormValue("token")
	score, ok := parseScore(r)
	msg := subscribedMsg
	if ok {
		// We need to update the score too.
		ok = ctx.db.updateScore(email, token, score)
		msg = scoreUpdatedMsg
	} else {
		ok = ctx.db.activate(email, token)
	}

	if ok {
		return writeMessage(msg, w)
	}
	return errMessage{errInvalidLink}
}

// ActivateHandler is the HTTP handler for managing account unsubscriptions; It handles '/unsubscribe'.
func UnsubscribeHandler(ctx *Context, w http.ResponseWriter, r *http.Request) error {
	switch r.Method {
	case "POST":
		email, ok := parseEmail(r)
		u, found := ctx.db.findUser(email)
		if !ok || !found {
			return errMessage{errNotFound}
		}

		u.Token = newToken() // reset user token.
		if err := ctx.db.updateToken(u.Id, u.Token); err != nil {
			return errInternal{err}
		}

		q := url.Values{}
		q.Set("email", u.Email)
		q.Set("token", u.Token)
		link := config.Url + "/unsubscribe?" + q.Encode()
		go sendUnsubscription(email, link)

		return writeMessage(linkSentMsg, w)
	case "GET":
		email, token := r.FormValue("email"), r.FormValue("token")
		if ctx.db.unsubscribe(email, token) {
			return writeMessage(unsubscribedMsg, w)
		}
		return errMessage{errInvalidLink}
	}
	return nil
}

// writeMessage renders a message in the default 'info' template.
func writeMessage(msg string, w http.ResponseWriter) error {
	return useTemplate("info", msg, w)
}

// parseEmail gets the email attribute from the request.
func parseEmail(r *http.Request) (string, bool) {
	email := r.FormValue("email")
	return email, validateAddress(email)
}

// parseScore gets the score attribute from the request.
func parseScore(r *http.Request) (int, bool) {
	score, err := strconv.Atoi(r.FormValue("score"))
	return score, err == nil
}
