package services

import (
	"strings"
	"sync"
	"time"

	database "uchoastock/backend/database"
	"uchoastock/backend/models"

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
		// Email que não existe também passa por uma conferência de bcrypt,
		// contra um hash qualquer. Sem isso, a resposta para um email
		// inexistente vinha bem mais rápido (o bcrypt é lento de
		// propósito), e medir o tempo revelava quais emails têm conta.
		_ = bcrypt.CompareHashAndPassword(missingUserHash(), []byte(password))
		return nil, false
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		return nil, false
	}

	return &user, true
}

var (
	missingUserHashOnce  sync.Once
	missingUserHashValue []byte
)

// missingUserHash devolve um hash bcrypt fixo, gerado uma vez só (na
// primeira tentativa com email inexistente), com o mesmo custo dos hashes
// de verdade: conferir contra ele demora o mesmo tempo.
func missingUserHash() []byte {
	missingUserHashOnce.Do(func() {
		missingUserHashValue, _ = bcrypt.GenerateFromPassword([]byte("conta-inexistente"), bcrypt.DefaultCost)
	})
	return missingUserHashValue
}

// ---------------------------------------------------------------------
// Espera depois de tentativas erradas
// ---------------------------------------------------------------------

// Depois de maxLoginAttempts erros seguidos no mesmo email, ou de
// maxIPLoginAttempts erros vindos do mesmo endereço, é preciso esperar
// antes de tentar de novo. A espera começa em loginLockWindow e dobra a
// cada bloqueio seguido, até maxLoginLockWindow.
//
// A contagem vive na memória do processo de propósito: ela defende
// contra tentativa em sequência, e não precisa sobreviver a um reinício.
// Guardar isso no banco custaria uma escrita a cada senha errada.
//
// Por que as três regras:
//   - por email, porque é a conta que se quer proteger;
//   - por endereço, porque só por email quem testa uma senha comum em
//     muitas contas diferentes nunca era barrado;
//   - espera que dobra, porque com uma espera fixa de 30 segundos dava
//     para testar umas 14 mil senhas por dia na mesma conta. Dobrando até
//     15 minutos, isso cai para umas 500.
//
// O efeito colateral é que alguém que saiba o email de outra pessoa
// consegue mantê-la esperando, errando de propósito. Um login certo zera
// a contagem do email.
const (
	maxLoginAttempts   = 5
	maxIPLoginAttempts = 20
	loginLockWindow    = 30 * time.Second
	maxLoginLockWindow = 15 * time.Minute

	// Limites da faxina do mapa. Sem eles, tentativas com emails
	// inventados fariam o mapa crescer sem limite.
	maxTrackedLogins = 1024
	loginAttemptTTL  = 30 * time.Minute
)

type loginAttempt struct {
	failures int
	// locks é quantos bloqueios seguidos já houve: decide o tamanho da
	// próxima espera.
	locks      int
	releasedAt time.Time
	updatedAt  time.Time
}

var (
	loginAttemptsMutex sync.Mutex
	loginAttempts      = map[string]*loginAttempt{}
)

// loginKeys devolve as chaves da contagem: a do email e, quando se sabe o
// endereço de origem, a dele. O prefixo impede um email de colidir com um
// endereço.
func loginKeys(email, ip string) []loginKeyLimit {
	keys := []loginKeyLimit{{"email:" + strings.ToLower(strings.TrimSpace(email)), maxLoginAttempts}}
	if ip != "" {
		keys = append(keys, loginKeyLimit{"ip:" + ip, maxIPLoginAttempts})
	}
	return keys
}

// loginKeyLimit é uma chave da contagem e quantos erros ela tolera.
type loginKeyLimit struct {
	key   string
	limit int
}

// LoginLockSeconds informa quantos segundos faltam para poder tentar de
// novo com esse email, a partir desse endereço (ip "" ignora o endereço).
// Zero significa liberado.
func LoginLockSeconds(email, ip string) int {
	loginAttemptsMutex.Lock()
	defer loginAttemptsMutex.Unlock()

	seconds := 0
	for _, k := range loginKeys(email, ip) {
		seconds = max(seconds, remainingLockSeconds(k.key))
	}
	return seconds
}

// RegisterFailedLogin conta mais um erro no email e no endereço e devolve
// quantos segundos a pessoa vai esperar. Zero significa que ainda restam
// tentativas.
func RegisterFailedLogin(email, ip string) int {
	loginAttemptsMutex.Lock()
	defer loginAttemptsMutex.Unlock()

	pruneLoginAttempts()

	seconds := 0
	for _, k := range loginKeys(email, ip) {
		seconds = max(seconds, registerFailure(k.key, k.limit))
	}
	return seconds
}

// registerFailure conta um erro numa chave e devolve a espera dela.
// Exige o mutex preso por quem chama.
func registerFailure(key string, limit int) int {
	if seconds := remainingLockSeconds(key); seconds > 0 {
		return seconds
	}

	attempt := loginAttempts[key]
	if attempt == nil {
		attempt = &loginAttempt{}
		loginAttempts[key] = attempt
	}

	attempt.failures++
	attempt.updatedAt = time.Now()

	if attempt.failures < limit {
		return 0
	}

	// Ao bloquear, o contador de erros volta a zero: terminada a espera, a
	// pessoa recomeça com o número cheio de tentativas. O de bloqueios não
	// volta: a próxima espera é o dobro desta.
	window := loginLockWindow << attempt.locks
	if window > maxLoginLockWindow || window <= 0 {
		window = maxLoginLockWindow
	}
	attempt.failures = 0
	attempt.locks++
	attempt.releasedAt = time.Now().Add(window)

	return int(window / time.Second)
}

// ClearLoginAttempts esquece os erros de um email, depois de um login
// que deu certo. A contagem do endereço continua: senão quem tem uma
// conta válida zeraria o limite do endereço entrando nela entre uma
// rodada de tentativas e outra.
func ClearLoginAttempts(email string) {
	loginAttemptsMutex.Lock()
	defer loginAttemptsMutex.Unlock()

	delete(loginAttempts, loginKeys(email, "")[0].key)
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
