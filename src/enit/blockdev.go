package main

import (
	"os/exec"
	"strings"
)

type BlockDevice struct {
	Device    string
	UUID      string
	PartUUID  string
	Label     string
	PartLabel string
	Type      string
}

func GetBlockDevices() []BlockDevice {
	cmd := exec.Command("/sbin/blkid")
	out, err := cmd.Output()
	if err != nil {
		return make([]BlockDevice, 0)
	}

	blockDevices := make([]BlockDevice, 0)

	for _, line := range strings.Split(string(out), "\n") {
		line := strings.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		lineSplit := strings.SplitN(line, ": ", 2)
		if len(lineSplit) != 2 {
			return make([]BlockDevice, 0)
		}
		device := lineSplit[0]
		line = lineSplit[1]

		fields := []string{}
		sb := &strings.Builder{}
		quoted := false
		for _, r := range line {
			if r == '"' {
				quoted = !quoted
			} else if !quoted && r == ' ' {
				fields = append(fields, sb.String())
				sb.Reset()
			} else {
				sb.WriteRune(r)
			}
		}
		if sb.Len() > 0 {
			fields = append(fields, sb.String())
		}

		bd := BlockDevice{Device: device}

		for _, field := range fields {
			fieldSplit := strings.SplitN(field, "=", 2)
			if len(fieldSplit) != 2 {
				return make([]BlockDevice, 0)
			}
			fieldName := fieldSplit[0]
			fieldValue := fieldSplit[1]

			switch fieldName {
			case "UUID":
				bd.UUID = fieldValue
			case "PARTUUID":
				bd.PartUUID = fieldValue
			case "LABEL":
				bd.Label = fieldValue
			case "PARTLABEL":
				bd.PartLabel = fieldValue
			case "TYPE":
				bd.Type = fieldValue
			}
		}

		blockDevices = append(blockDevices, bd)
	}

	return blockDevices
}
