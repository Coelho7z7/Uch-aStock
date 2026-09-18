package main

import (
	"database/sql"
	"fmt"
	"strings"

	_ "gosqlite.org"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	db, err := sql.Open("sqlite", "backend/data/uchoastock.db")
	if err != nil {
		panic(err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT email, role, senha FROM usuarios ORDER BY id`)
	if err != nil {
		panic(err)
	}
	defer rows.Close()

	for rows.Next() {
		var email, role, hash string
		if err := rows.Scan(&email, &role, &hash); err != nil {
			panic(err)
		}
		account := strings.Split(email, "@")[0]
		expected := "@" + account + "12e"
		ok := bcrypt.CompareHashAndPassword([]byte(hash), []byte(expected)) == nil
		fmt.Printf("%-22s %-11s senha-padrao-confere=%v\n", email, role, ok)
	}
}
