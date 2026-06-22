package taskvisor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWrapCommand_LocalNoOp(t *testing.T) {
	er := LocalExecRuntime()
	assert.Equal(t, "composer install", wrapCommand("composer install", er))
	assert.Equal(t, "npx playwright test", wrapCommand("npx playwright test", er))
}

func TestWrapCommand_DockerPHP(t *testing.T) {
	er := ExecRuntime{RunTarget: "docker", AppSvc: "app", NodeSvc: "e2e"}
	assert.Equal(t, "docker compose exec -T app sh -c 'composer install'",
		wrapCommand("composer install", er))
	assert.Equal(t, "docker compose exec -T app sh -c 'vendor/bin/phpstan analyse --level=9'",
		wrapCommand("vendor/bin/phpstan analyse --level=9", er))
	assert.Equal(t, "docker compose exec -T app sh -c 'bin/console doctrine:fixtures:load --env=test'",
		wrapCommand("bin/console doctrine:fixtures:load --env=test", er))
}

func TestWrapCommand_DockerNode(t *testing.T) {
	er := ExecRuntime{RunTarget: "docker", AppSvc: "app", NodeSvc: "e2e"}
	assert.Equal(t, "docker compose exec -T e2e sh -c 'npx playwright test'",
		wrapCommand("npx playwright test", er))
}

func TestWrapCommand_DockerPinsComposeProject(t *testing.T) {
	er := ExecRuntime{RunTarget: "docker", AppSvc: "app", NodeSvc: "e2e", ComposeProject: "previo2"}
	assert.Equal(t, "docker compose -p previo2 exec -T app sh -c 'composer install'",
		wrapCommand("composer install", er))
	assert.Equal(t, "docker compose -p previo2 exec -T e2e sh -c 'npx playwright test'",
		wrapCommand("npx playwright test", er))
}

func TestWrapCommand_DockerNoProject_BareFormatUnchanged(t *testing.T) {
	er := ExecRuntime{RunTarget: "docker", AppSvc: "app"} // ComposeProject empty
	assert.Equal(t, "docker compose exec -T app sh -c 'composer install'",
		wrapCommand("composer install", er))
}

func TestWrapCommand_DockerNode_NoNodeSvc_Unchanged(t *testing.T) {
	er := ExecRuntime{RunTarget: "docker", AppSvc: "app"} // NodeSvc empty
	assert.Equal(t, "npx playwright test", wrapCommand("npx playwright test", er))
}

func TestWrapCommand_HostCommandsUnchanged(t *testing.T) {
	er := ExecRuntime{RunTarget: "docker", AppSvc: "app", NodeSvc: "e2e"}
	for _, c := range []string{
		`curl -s http://localhost:8080/health`,
		`test -f composer.json`,
		`grep -q foo bar`,
		`python3 -m json.tool`,
		`ls -la`,
	} {
		assert.Equal(t, c, wrapCommand(c, er), c)
	}
}

func TestWrapCommand_Idempotent_AlreadyDocker(t *testing.T) {
	er := ExecRuntime{RunTarget: "docker", AppSvc: "app", NodeSvc: "e2e"}
	pre := "docker compose exec -T app composer install"
	assert.Equal(t, pre, wrapCommand(pre, er))
}

func TestWrapCommand_Compound(t *testing.T) {
	er := ExecRuntime{RunTarget: "docker", AppSvc: "app"}
	assert.Equal(t, `docker compose exec -T app sh -c 'vendor/bin/phpstan analyse && vendor/bin/ecs check src/'`,
		wrapCommand("vendor/bin/phpstan analyse && vendor/bin/ecs check src/", er))
}

func TestWrapCommand_EnvAssignPrefix(t *testing.T) {
	er := ExecRuntime{RunTarget: "docker", AppSvc: "app"}
	assert.Equal(t, `docker compose exec -T app sh -c 'APP_ENV=test bin/console doctrine:fixtures:load'`,
		wrapCommand("APP_ENV=test bin/console doctrine:fixtures:load", er))
}

func TestWrapCommand_SingleQuoteEscaping(t *testing.T) {
	er := ExecRuntime{RunTarget: "docker", AppSvc: "app"}
	assert.Equal(t, `docker compose exec -T app sh -c 'php -r '\''echo 1;'\'''`,
		wrapCommand(`php -r 'echo 1;'`, er))
}

func TestWrapCommand_EmptyUnchanged(t *testing.T) {
	er := ExecRuntime{RunTarget: "docker", AppSvc: "app"}
	assert.Equal(t, "", wrapCommand("", er))
}
