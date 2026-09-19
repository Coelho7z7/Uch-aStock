package services

import (
	"database/sql"
	"fmt"
	"time"

	// Embute no binário a tabela de fusos horários. Sem ela, carregar
	// "America/Sao_Paulo" depende de o servidor ter essa tabela instalada,
	// e a imagem do Railway pode não ter.
	_ "time/tzdata"
)

// TimezoneName é o fuso em que o sistema mostra datas e decide o que é
// "hoje". As obras ficam no Brasil, então ele é fixo: não depende do fuso
// configurado no servidor (no Railway, o padrão é UTC).
const TimezoneName = "America/Sao_Paulo"

// dbTimeLayout é o formato em que o SQLite grava CURRENT_TIMESTAMP, sempre
// em UTC. Datas nesse formato, comparadas como texto, ficam na ordem certa.
const dbTimeLayout = "2006-01-02 15:04:05"

// SetupTimezone faz do fuso de Brasília o horário local do processo
// (time.Local). Roda no início do main, antes de qualquer data ser
// formatada.
func SetupTimezone() error {
	location, err := time.LoadLocation(TimezoneName)
	if err != nil {
		return fmt.Errorf("carregar o fuso %s: %w", TimezoneName, err)
	}
	time.Local = location
	return nil
}

// dayStartUTC devolve o instante em que o dia date (AAAA-MM-DD, no
// calendário local) começa, já em UTC e no formato do banco. offsetDays
// desloca o dia: 1 dá o começo do dia seguinte, que é o fim exclusivo de
// um período.
//
// Assim o filtro por dia vira "data >= início AND data < fim", comparando
// direto com o que está gravado. Antes era date(data, 'localtime'), que
// depende do fuso do servidor.
func dayStartUTC(date string, offsetDays int) (string, error) {
	day, err := time.ParseInLocation("2006-01-02", date, time.Local)
	if err != nil {
		return "", fmt.Errorf("data inválida: %s", date)
	}
	return day.AddDate(0, 0, offsetDays).UTC().Format(dbTimeLayout), nil
}

// todayRangeUTC devolve o começo de hoje e o de amanhã (calendário local),
// em UTC, para contar o que aconteceu hoje.
func todayRangeUTC() (string, string) {
	today := time.Now().In(time.Local).Format("2006-01-02")
	start, _ := dayStartUTC(today, 0)
	end, _ := dayStartUTC(today, 1)
	return start, end
}

// formatDBTime lê uma data gravada pelo banco (em UTC) e a escreve no
// horário local, no layout pedido. Data vazia (coluna NULL) ou num
// formato desconhecido devolve "".
func formatDBTime(raw sql.NullString, layout string) string {
	if !raw.Valid || raw.String == "" {
		return ""
	}
	parsed, err := parseMovementDate(raw.String)
	if err != nil {
		return ""
	}
	return parsed.In(time.Local).Format(layout)
}
