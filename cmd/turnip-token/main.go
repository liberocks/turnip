package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/liberocks/turnip/internal/turnip"
)

func main() {
	subject := flag.String("subject", "", "JWT subject (required)")
	room := flag.String("room", "", "authorized room ID (required)")
	name := flag.String("name", "", "optional display name")
	lifetime := flag.Duration("lifetime", 15*time.Minute, "token lifetime")
	flag.Parse()
	secret := os.Getenv("TURNIP_JWT_SECRET")
	issuer := os.Getenv("TURNIP_JWT_ISSUER")
	audience := os.Getenv("TURNIP_JWT_AUDIENCE")
	if len(secret) < 32 || issuer == "" || audience == "" || *subject == "" || *room == "" || *lifetime <= 0 {
		_, _ = fmt.Fprintln(os.Stderr, "set TURNIP_JWT_SECRET (32+ bytes), TURNIP_JWT_ISSUER, and TURNIP_JWT_AUDIENCE; pass -subject and -room")
		os.Exit(2)
	}
	token, err := turnip.GenerateToken(secret, issuer, audience, *subject, *room, *name, *lifetime, time.Now())
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "generate token:", err)
		os.Exit(1)
	}
	_, _ = fmt.Println(token)
}
