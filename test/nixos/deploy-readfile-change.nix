{ pkgs, mgmt }:
let
  mkDeploy = name: value:
    pkgs.runCommand name {} ''
      mkdir -p "$out/files"
      cat >"$out/main.mcl" <<'EOF'
      import "deploy"

      file "/tmp/deploy-readfile-output" {
        state => $const.res.file.state.exists,
        content => deploy.readfile("/files/value.txt"),
      }
      EOF
      printf 'main: main.mcl\n' >"$out/metadata.yaml"
      printf '%s' '${value}' >"$out/files/value.txt"
    '';

  deployAlpha = mkDeploy "mgmt-deploy-readfile-alpha" "alpha";
  deployBravo = mkDeploy "mgmt-deploy-readfile-bravo" "bravo";
in
pkgs.testers.runNixOSTest {
  name = "mgmt-deploy-readfile-change";

  nodes = {
    target = { pkgs, ... }: {
      environment.systemPackages = [
        mgmt
        pkgs.curl
      ];

      networking.firewall.allowedTCPPorts = [ 2379 ];

      system.stateVersion = "24.11";
    };

    deployer = { pkgs, ... }: {
      environment.systemPackages = [
        mgmt
        pkgs.curl
      ];

      system.stateVersion = "24.11";
    };
  };

  testScript = ''
    start_all()

    target.wait_for_unit("multi-user.target")
    deployer.wait_for_unit("multi-user.target")

    with subtest("start target mgmt instance"):
        target.succeed("set -euo pipefail; ${mgmt}/bin/mgmt run --hostname deploy-readfile-target --tmp-prefix --no-pgp --client-urls=http://192.168.1.2:2379 --server-urls=http://192.168.1.2:2380 --converged-timeout=-1 empty >/tmp/mgmt-run.log 2>&1 & echo $! >/tmp/mgmt-run.pid; sleep 2; kill -0 $(cat /tmp/mgmt-run.pid)")
        target.succeed("for i in $(seq 1 30); do curl -fsS http://192.168.1.2:2379/health && exit 0; sleep 1; done; echo 'target mgmt health never became ready' >&2; cat /tmp/mgmt-run.log >&2; exit 1")
        deployer.succeed("for i in $(seq 1 30); do curl -fsS http://192.168.1.2:2379/health && exit 0; sleep 1; done; echo 'deployer could not reach target mgmt health endpoint' >&2; exit 1")

    with subtest("deploy first graph from separate controller node"):
        deployer.succeed("${mgmt}/bin/mgmt deploy --no-git --seeds=http://192.168.1.2:2379 lang ${deployAlpha}/")
        target.succeed("for i in $(seq 1 30); do test \"$(cat /tmp/deploy-readfile-output 2>/dev/null || true)\" = alpha && exit 0; sleep 1; done; echo 'alpha deploy never reconciled' >&2; cat /tmp/mgmt-run.log >&2; exit 1")

    with subtest("deploy updated graph from separate controller node"):
        deployer.succeed("${mgmt}/bin/mgmt deploy --no-git --seeds=http://192.168.1.2:2379 lang ${deployBravo}/")
        target.succeed("for i in $(seq 1 30); do test \"$(cat /tmp/deploy-readfile-output 2>/dev/null || true)\" = bravo && exit 0; sleep 1; done; echo 'bravo deploy never reconciled' >&2; cat /tmp/mgmt-run.log >&2; exit 1")

    target.succeed("kill $(cat /tmp/mgmt-run.pid)")
  '';
}
