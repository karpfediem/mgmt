// Mgmt
// Copyright (C) James Shubin and the project contributors
// Written by James Shubin <james@shubin.ca> and the project contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.
//
// Additional permission under GNU GPL version 3 section 7
//
// If you modify this program, or any covered work, by linking or combining it
// with embedded mcl code and modules (and that the embedded mcl code and
// modules which link with this program, contain a copy of their source code in
// the authoritative form) containing parts covered by the terms of any other
// license, the licensors of this program grant you additional permission to
// convey the resulting work. Furthermore, the licensors of this program grant
// the original author, James Shubin, additional permission to update this
// additional permission if he deems it necessary to achieve the goals of this
// additional permission.

package ssh

import (
	"os"

	sshconfig "github.com/kevinburke/ssh_config"

	"github.com/purpleidea/mgmt/util"
)

type sshConfigValues struct {
	HostName      string
	User          string
	Port          string
	IdentityFiles []string
}

func loadSSHConfigFile(filename string) (*sshconfig.Config, error) {
	p, err := util.ExpandHome(filename)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	return sshconfig.DecodeBytes(data)
}

func lookupSSHConfigValue(alias, key string, configs ...*sshconfig.Config) (string, error) {
	for _, config := range configs {
		if config == nil {
			continue
		}

		value, err := config.Get(alias, key)
		if err != nil {
			return "", err
		}
		if value != "" {
			return value, nil
		}
	}

	return "", nil
}

func lookupSSHConfigValues(alias, key string, configs ...*sshconfig.Config) ([]string, error) {
	for _, config := range configs {
		if config == nil {
			continue
		}

		values, err := config.GetAll(alias, key)
		if err != nil {
			return nil, err
		}
		if len(values) > 0 {
			return values, nil
		}
	}

	return nil, nil
}

func resolveSSHConfigValues(alias string, userConfig, systemConfig *sshconfig.Config) (sshConfigValues, error) {
	values := sshConfigValues{}

	hostName, err := lookupSSHConfigValue(alias, "HostName", userConfig, systemConfig)
	if err != nil {
		return values, err
	}
	values.HostName = hostName

	user, err := lookupSSHConfigValue(alias, "User", userConfig, systemConfig)
	if err != nil {
		return values, err
	}
	values.User = user

	port, err := lookupSSHConfigValue(alias, "Port", userConfig, systemConfig)
	if err != nil {
		return values, err
	}
	values.Port = port

	identityFiles, err := lookupSSHConfigValues(alias, "IdentityFile", userConfig, systemConfig)
	if err != nil {
		return values, err
	}
	values.IdentityFiles = identityFiles

	return values, nil
}

func (obj *World) sshConfigValues(alias string) sshConfigValues {
	values := sshConfigValues{}
	if alias == "" {
		return values
	}

	userConfig, err := loadSSHConfigFile(defaultSSHDir + "config")
	if err != nil {
		if obj.init.Debug {
			obj.init.Logf("ssh config user file ignored: %v", err)
		}
		userConfig = nil
	}

	systemConfig, err := loadSSHConfigFile("/etc/ssh/ssh_config")
	if err != nil {
		if obj.init.Debug {
			obj.init.Logf("ssh config system file ignored: %v", err)
		}
		systemConfig = nil
	}

	values, err = resolveSSHConfigValues(alias, userConfig, systemConfig)
	if err != nil {
		if obj.init.Debug {
			obj.init.Logf("ssh config resolution ignored: %v", err)
		}
		return sshConfigValues{}
	}

	return values
}
