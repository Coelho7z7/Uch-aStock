package services

import (
	"strings"

	database "uchoastock/backend/database"
	"uchoastock/backend/models"
)

// Valores do filtro de situação da lista de fornecedores (?situacao=).
const (
	SupplierFilterActive   = "ATIVO"
	SupplierFilterInactive = "INATIVO"
)

// SupplierStatusLabels liga cada valor do filtro ao texto da tela.
var SupplierStatusLabels = []struct{ Value, Label string }{
	{SupplierFilterActive, "Ativos"},
	{SupplierFilterInactive, "Desativados"},
}

// supplierColumns são as colunas lidas em toda consulta de fornecedor, na
// mesma ordem em que scanSupplier as espera.
const supplierColumns = `id, nome, cnpj, contato, telefone, email, cidade, observacao, ativo`

func scanSupplier(row rowScanner) (models.Supplier, error) {
	var s models.Supplier
	if err := row.Scan(
		&s.ID,
		&s.Name,
		&s.CNPJ,
		&s.Contact,
		&s.Phone,
		&s.Email,
		&s.City,
		&s.Note,
		&s.Active,
	); err != nil {
		return s, err
	}
	s.FormattedCNPJ = formatCNPJ(s.CNPJ)
	return s, nil
}

// PaginatedSuppliers lista os fornecedores, os ativos primeiro e depois
// por nome, e devolve também o total (para a paginação). search procura
// no nome, no CNPJ (com ou sem pontuação), no contato, na cidade e no
// email; status vazio ou desconhecido traz ativos e desativados.
func PaginatedSuppliers(search, status string, page, perPage int) ([]models.Supplier, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 10
	}

	where := ` WHERE 1 = 1`
	var args []any

	if search = strings.TrimSpace(search); search != "" {
		pattern := "%" + search + "%"
		where += ` AND (nome LIKE ? OR contato LIKE ? OR cidade LIKE ? OR email LIKE ?`
		args = append(args, pattern, pattern, pattern, pattern)

		// O CNPJ fica gravado sem pontuação: "12.345" vira "12345" para
		// casar com ele.
		cnpj := strings.NewReplacer(".", "", "/", "", "-", "", " ", "").Replace(strings.ToUpper(search))
		if cnpj != "" {
			where += ` OR cnpj LIKE ?`
			args = append(args, "%"+cnpj+"%")
		}
		where += `)`
	}

	switch status {
	case SupplierFilterActive:
		where += ` AND ativo = 1`
	case SupplierFilterInactive:
		where += ` AND ativo = 0`
	}

	var total int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM fornecedores`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := database.DB.Query(
		`SELECT `+supplierColumns+` FROM fornecedores`+where+`
		ORDER BY ativo DESC, nome COLLATE NOCASE
		LIMIT ? OFFSET ?`,
		append(args, perPage, (page-1)*perPage)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var suppliers []models.Supplier
	for rows.Next() {
		s, err := scanSupplier(rows)
		if err != nil {
			return nil, 0, err
		}
		suppliers = append(suppliers, s)
	}
	return suppliers, total, rows.Err()
}

// GetSupplierByID busca um fornecedor, ativo ou não. Devolve
// sql.ErrNoRows se ele não existir.
func GetSupplierByID(id int) (*models.Supplier, error) {
	s, err := scanSupplier(database.DB.QueryRow(`SELECT `+supplierColumns+` FROM fornecedores WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	return &s, nil
}
