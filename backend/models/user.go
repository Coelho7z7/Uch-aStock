package models

// User é uma conta do sistema. SiteID é a obra em que a pessoa atua
// (0 quando não tem vínculo — o caso de administradores, que veem
// todas); SiteName é o nome dessa obra, para mostrar na tela.
type User struct {
	ID       int
	Name     string
	Email    string
	Role     string
	Active   bool
	SiteID   int
	SiteName string
}
