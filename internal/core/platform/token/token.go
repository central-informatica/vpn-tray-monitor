// Package token ajusta o token do próprio processo.
//
// SetDefaultOwnerAdmins existe por causa da política do Windows "Objetos do
// sistema: proprietário padrão para objetos criados por membros do grupo
// Administradores", cujo padrão (do Vista em diante) é "Criador do objeto":
// um processo administrador elevado cria arquivos e pastas com dono = a
// conta do usuário, não Administradores. A pasta de dados (acl, §5.1) só
// confia em filhos com dono Administradores ou SYSTEM; sem este ajuste, o que
// a CLI elevada grava (cofre, config, log do `run`) levaria a pasta inteira à
// quarentena na partida seguinte do serviço.
package token
