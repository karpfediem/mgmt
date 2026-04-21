{ pkgs, mgmt }:
let
  serviceName = "deploy-restart-proof";
  serviceUnitPath = "/run/systemd/system/${serviceName}.service";
  runtimeValuePath = "/run/${serviceName}/value";

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
      refresh_action => "try-restart",
    }

    File["${serviceUnitPath}"] -> Exec["systemctl daemon-reload"] -> Svc["${serviceName}"]
  '';

  mkMetadata = pkgs.writeText "metadata.yaml" ''
    main: main.mcl
  '';

  mkDeploy = name: value:
    let
      serviceScript = pkgs.writeShellScript "${serviceName}-${value}" ''
        printf '%s' '${value}' > ${runtimeValuePath}
        exec /run/current-system/sw/bin/sleep infinity
      '';
      unitFile = pkgs.writeText "${serviceName}.service" ''
        [Unit]
        Description=Deploy restart proof service
        After=network.target

        [Service]
        Type=simple
        RuntimeDirectory=${serviceName}
        ExecStart=${serviceScript}
        Restart=no
      '';
    in
    pkgs.runCommand name {} ''
      mkdir -p "$out/files"
      cp ${mkMain} "$out/main.mcl"
      cp ${unitFile} "$out/files/${serviceName}.service"
      cp ${mkMetadata} "$out/metadata.yaml"
    '';

  deployAlpha = mkDeploy "mgmt-deploy-svc-restart-alpha" "alpha";
  deployBravo = mkDeploy "mgmt-deploy-svc-restart-bravo" "bravo";
in
pkgs.testers.runNixOSTest {
  name = "mgmt-deploy-svc-restart-change";

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
        target.succeed("set -euo pipefail; ${mgmt}/bin/mgmt run --hostname deploy-restart-target --tmp-prefix --no-pgp --client-urls=http://192.168.1.2:2379 --server-urls=http://192.168.1.2:2380 --converged-timeout=-1 empty >/tmp/mgmt-run.log 2>&1 & echo $! >/tmp/mgmt-run.pid; sleep 2; kill -0 $(cat /tmp/mgmt-run.pid)")
        target.succeed("for i in $(seq 1 30); do curl -fsS http://192.168.1.2:2379/health && exit 0; sleep 1; done; echo 'target mgmt health never became ready' >&2; cat /tmp/mgmt-run.log >&2; exit 1")
        deployer.succeed("for i in $(seq 1 30); do curl -fsS http://192.168.1.2:2379/health && exit 0; sleep 1; done; echo 'deployer could not reach target mgmt health endpoint' >&2; exit 1")

    with subtest("deploy initial service and capture pid"):
        deployer.succeed("${mgmt}/bin/mgmt deploy --no-git --seeds=http://192.168.1.2:2379 lang ${deployAlpha}/")
        target.succeed("for i in $(seq 1 30); do systemctl is-active --quiet ${serviceName}.service && test \"$(cat ${runtimeValuePath} 2>/dev/null || true)\" = alpha && exit 0; sleep 1; done; echo 'initial service deploy never converged' >&2; systemctl status ${serviceName}.service >&2 || true; cat /tmp/mgmt-run.log >&2; exit 1")
        initial_pid = target.succeed("systemctl show -P MainPID ${serviceName}.service").strip()
        initial_start = target.succeed("systemctl show -P ExecMainStartTimestampMonotonic ${serviceName}.service").strip()
        assert initial_pid != "0", f"unexpected initial MainPID: {initial_pid}"

    with subtest("deploy updated unit and verify restart"):
        deployer.succeed("${mgmt}/bin/mgmt deploy --no-git --seeds=http://192.168.1.2:2379 lang ${deployBravo}/")
        target.succeed("for i in $(seq 1 30); do systemctl is-active --quiet ${serviceName}.service && test \"$(cat ${runtimeValuePath} 2>/dev/null || true)\" = bravo && exit 0; sleep 1; done; echo 'updated service value never converged' >&2; systemctl status ${serviceName}.service >&2 || true; cat /tmp/mgmt-run.log >&2; exit 1")
        target.succeed(f"for i in $(seq 1 30); do pid=\"$(systemctl show -P MainPID ${serviceName}.service)\"; start=\"$(systemctl show -P ExecMainStartTimestampMonotonic ${serviceName}.service)\"; if [ \"$pid\" != \"{initial_pid}\" ] && [ \"$start\" != \"{initial_start}\" ]; then exit 0; fi; sleep 1; done; echo 'service never restarted after deploy update' >&2; systemctl status ${serviceName}.service >&2 || true; cat /tmp/mgmt-run.log >&2; exit 1")

    target.succeed("kill $(cat /tmp/mgmt-run.pid)")
  '';
}
