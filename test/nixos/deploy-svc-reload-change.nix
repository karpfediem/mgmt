{ pkgs, mgmt }:
let
  serviceName = "deploy-reload-proof";
  serviceUnitPath = "/run/systemd/system/${serviceName}.service";
  runtimeValuePath = "/run/${serviceName}/value";
  reloadCountPath = "/run/${serviceName}/reload-count";

  mkMain = pkgs.writeText "main.mcl" ''
    import "deploy"

    file "${serviceUnitPath}" {
      state => $const.res.file.state.exists,
      content => deploy.readfile("/files/${serviceName}.service"),
      Notify => Svc["${serviceName}"],
    }

    exec "systemctl daemon-reload" {
      cmd => "/run/current-system/sw/bin/systemctl",
      args => ["daemon-reload",],
      mtimes => ["${serviceUnitPath}",],
      ifcmd => "/run/current-system/sw/bin/true",
    }

    svc "${serviceName}" {
      state => "running",
      refresh_action => "reload-or-try-restart",
    }

    File["${serviceUnitPath}"] -> Exec["systemctl daemon-reload"] -> Svc["${serviceName}"]
  '';

  mkMetadata = pkgs.writeText "metadata.yaml" ''
    main: main.mcl
  '';

  mkDeploy = name: value:
    let
      startScript = pkgs.writeShellScript "${serviceName}-start-${value}" ''
        printf '%s' '${value}' > ${runtimeValuePath}
        exec /run/current-system/sw/bin/sleep infinity
      '';
      reloadScript = pkgs.writeShellScript "${serviceName}-reload-${value}" ''
        current=0
        if [ -f ${reloadCountPath} ]; then
          current="$(cat ${reloadCountPath})"
        fi
        next=$((current + 1))
        printf '%s' '${value}' > ${runtimeValuePath}
        printf '%s' "$next" > ${reloadCountPath}
      '';
      unitFile = pkgs.writeText "${serviceName}.service" ''
        [Unit]
        Description=Deploy reload proof service
        After=network.target

        [Service]
        Type=simple
        RuntimeDirectory=${serviceName}
        ExecStart=${startScript}
        ExecReload=${reloadScript}
        Restart=no
      '';
    in
    pkgs.runCommand name {} ''
      mkdir -p "$out/files"
      cp ${mkMain} "$out/main.mcl"
      cp ${unitFile} "$out/files/${serviceName}.service"
      cp ${mkMetadata} "$out/metadata.yaml"
    '';

  deployAlpha = mkDeploy "mgmt-deploy-svc-reload-alpha" "alpha";
  deployBravo = mkDeploy "mgmt-deploy-svc-reload-bravo" "bravo";
in
pkgs.testers.runNixOSTest {
  name = "mgmt-deploy-svc-reload-change";

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
        target.succeed("set -euo pipefail; ${mgmt}/bin/mgmt run --hostname deploy-reload-target --tmp-prefix --no-pgp --client-urls=http://192.168.1.2:2379 --server-urls=http://192.168.1.2:2380 --converged-timeout=-1 empty >/tmp/mgmt-run.log 2>&1 & echo $! >/tmp/mgmt-run.pid; sleep 2; kill -0 $(cat /tmp/mgmt-run.pid)")
        target.succeed("for i in $(seq 1 30); do curl -fsS http://192.168.1.2:2379/health && exit 0; sleep 1; done; echo 'target mgmt health never became ready' >&2; cat /tmp/mgmt-run.log >&2; exit 1")
        deployer.succeed("for i in $(seq 1 30); do curl -fsS http://192.168.1.2:2379/health && exit 0; sleep 1; done; echo 'deployer could not reach target mgmt health endpoint' >&2; exit 1")

    with subtest("deploy initial service and capture pid"):
        deployer.succeed("${mgmt}/bin/mgmt deploy --no-git --seeds=http://192.168.1.2:2379 lang ${deployAlpha}/")
        target.succeed("for i in $(seq 1 30); do systemctl is-active --quiet ${serviceName}.service && test \"$(cat ${runtimeValuePath} 2>/dev/null || true)\" = alpha && exit 0; sleep 1; done; echo 'initial service deploy never converged' >&2; systemctl status ${serviceName}.service >&2 || true; cat /tmp/mgmt-run.log >&2; exit 1")
        target.succeed("test ! -e ${reloadCountPath}")
        initial_pid = target.succeed("systemctl show -P MainPID ${serviceName}.service").strip()
        initial_start = target.succeed("systemctl show -P ExecMainStartTimestampMonotonic ${serviceName}.service").strip()
        assert initial_pid != '0', f'unexpected initial MainPID: {initial_pid}'

    with subtest("deploy updated unit and verify reload without restart"):
        deployer.succeed("${mgmt}/bin/mgmt deploy --no-git --seeds=http://192.168.1.2:2379 lang ${deployBravo}/")
        target.succeed("for i in $(seq 1 30); do systemctl is-active --quiet ${serviceName}.service && test \"$(cat ${runtimeValuePath} 2>/dev/null || true)\" = bravo && test \"$(cat ${reloadCountPath} 2>/dev/null || true)\" = 1 && exit 0; sleep 1; done; echo 'updated service reload never converged' >&2; systemctl status ${serviceName}.service >&2 || true; cat /tmp/mgmt-run.log >&2; exit 1")
        target.succeed(f"pid=\"$(systemctl show -P MainPID ${serviceName}.service)\"; test \"$pid\" = \"{initial_pid}\"")
        target.succeed(f"start=\"$(systemctl show -P ExecMainStartTimestampMonotonic ${serviceName}.service)\"; test \"$start\" = \"{initial_start}\"")

    target.succeed("kill $(cat /tmp/mgmt-run.pid)")
  '';
}
