package services

import (
	"bufio"
	"fmt"
	"strings"
	"sync"
	"time"

	database "uchoastock/backend/database"
	"uchoastock/backend/models"
	"uchoastock/backend/utils"

	"golang.org/x/crypto/bcrypt"
)

func AuthenticateUser(email string, password string) (*models.User, bool) {
	email = strings.ToLower(strings.TrimSpace(email))

	query := `
		SELECT id, nome, senha, role
		FROM usuarios
		WHERE email = ? AND ativo = 1
	`

	var user models.User
	var passwordHash string

	err := database.DB.QueryRow(query, email).Scan(
		&user.ID,
		&user.Name,
		&passwordHash,
		&user.Role,
	)

	if err != nil {
		return nil, false
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		return nil, false
	}

	return &user, true
}

// ---------------------------------------------------------------------
// Espera depois de tentativas erradas
// ---------------------------------------------------------------------

// Depois de maxLoginAttempts erros seguidos, o email espera
// loginLockWindow antes de poder tentar de novo.
//
// A contagem vive na memória do processo de propósito: ela defende
// contra tentativa em sequência, que se mede em segundos, e não precisa
// sobreviver a um reinício — o processo cair já interrompe a sequência
// junto. Guardar isso no banco custaria uma escrita a cada senha errada
// sem proteger nada a mais.
//
// A conta é por email, não por origem da conexão, porque é a conta que
// se quer proteger. O efeito colateral é que alguém que saiba o email de
// outra pessoa consegue mantê-la esperando, errando de propósito; com
// uma janela de 30 segundos isso atrapalha, mas não tranca ninguém para
// fora.
const (
	maxLoginAttempts = 5
	loginLockWindow  = 30 * time.Second

	// Limites da faxina do mapa. Sem eles, tentativas com emails
	// inventados fariam o mapa crescer sem limite.
	maxTrackedLogins = 1024
	loginAttemptTTL  = 10 * time.Minute
)

type loginAttempt struct {
	failures   int
	releasedAt time.Time
	updatedAt  time.Time
}

var (
	loginAttemptsMutex sync.Mutex
	loginAttempts      = map[string]*loginAttempt{}
)

func loginAttemptKey(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// LoginLockSeconds informa quantos segundos faltam para o email poder
// tentar de novo. Zero significa liberado.
func LoginLockSeconds(email string) int {
	loginAttemptsMutex.Lock()
	defer loginAttemptsMutex.Unlock()

	return remainingLockSeconds(loginAttemptKey(email))
}

// RegisterFailedLogin conta mais um erro e devolve quantos segundos a
// pessoa vai esperar. Zero significa que ainda restam tentativas.
func RegisterFailedLogin(email string) int {
	key := loginAttemptKey(email)

	loginAttemptsMutex.Lock()
	defer loginAttemptsMutex.Unlock()

	if seconds := remainingLockSeconds(key); seconds > 0 {
		return seconds
	}

	pruneLoginAttempts()

	attempt := loginAttempts[key]
	if attempt == nil {
		attempt = &loginAttempt{}
		loginAttempts[key] = attempt
	}

	attempt.failures++
	attempt.updatedAt = time.Now()

	if attempt.failures < maxLoginAttempts {
		return 0
	}

	// Ao bloquear, o contador volta a zero: terminada a espera, a pessoa
	// recomeça com o número cheio de tentativas em vez de ser bloqueada
	// de novo no primeiro erro seguinte.
	attempt.failures = 0
	attempt.releasedAt = time.Now().Add(loginLockWindow)

	return int(loginLockWindow / time.Second)
}

// ClearLoginAttempts esquece os erros de um email, depois de um login
// que deu certo.
func ClearLoginAttempts(email string) {
	loginAttemptsMutex.Lock()
	defer loginAttemptsMutex.Unlock()

	delete(loginAttempts, loginAttemptKey(email))
}

// remainingLockSeconds arredonda para cima, para o contador da tela
// nunca chegar a zero enquanto o servidor ainda recusa. Exige o mutex
// preso por quem chama.
func remainingLockSeconds(key string) int {
	attempt := loginAttempts[key]
	if attempt == nil {
		return 0
	}

	remaining := time.Until(attempt.releasedAt)
	if remaining <= 0 {
		return 0
	}

	return int((remaining + time.Second - 1) / time.Second)
}

// pruneLoginAttempts descarta registros parados há tempo demais. Só roda
// quando o mapa passa do limite, então o caso normal não paga por ela.
// Exige o mutex preso por quem chama.
func pruneLoginAttempts() {
	if len(loginAttempts) < maxTrackedLogins {
		return
	}

	limit := time.Now().Add(-loginAttemptTTL)
	for key, attempt := range loginAttempts {
		if attempt.releasedAt.Before(time.Now()) && attempt.updatedAt.Before(limit) {
			delete(loginAttempts, key)
		}
	}
}

// Login continua sendo usado pelo sistema do terminal.
func Login(reader *bufio.Reader) (*models.User, bool) {
	email := strings.ToLower(strings.TrimSpace(utils.ReadText(reader, "Email: ")))
	password := utils.ReadText(reader, "Senha: ")

	user, success := AuthenticateUser(email, password)
	if !success {
		fmt.Println("Email ou senha incorretos.")
		return nil, false
	}

	fmt.Println("Login realizado com sucesso!")
	fmt.Println("Bem-vindo,", user.Name)

	return user, true
}
