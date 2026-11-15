package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

type gbPair struct {
	BudgetGB float64 `json:"budget_gb"`
	UsedGB   float64 `json:"used_gb"`
}

type hostEntry struct {
	Host       string  `json:"host"`
	Env        string  `json:"env"`
	State      string  `json:"state"`
	Memory     *gbPair `json:"memory"`
	HostMemory *gbPair `json:"host_memory"`
	Resident   []struct {
		Component string  `json:"component"`
		Model     string  `json:"model"`
		Variant   string  `json:"variant"`
		Precision string  `json:"precision"`
		GB        float64 `json:"gb"`
		Pinned    bool    `json:"pinned"`
		LastUsed  string  `json:"last_used"`
		Loras     []struct {
			Name *string `json:"name"`
		} `json:"loras"`
	} `json:"resident"`
}

// hostsCmd prints each host with its shared budget (host_memory), then its envs and what each holds.
func hostsCmd(stdout io.Writer) error {
	c, err := loggedIn()
	if err != nil {
		return err
	}
	var list struct {
		Hosts []hostEntry `json:"hosts"`
	}
	if err := c.call(http.MethodGet, "/v1/studio/hosts", nil, http.StatusOK, &list); err != nil {
		return err
	}
	last := ""
	for _, h := range list.Hosts {
		if h.Host != last {
			last = h.Host
			if h.HostMemory != nil {
				fmt.Fprintf(stdout, "%s  %.1f / %.1f GB\n", h.Host, h.HostMemory.UsedGB, h.HostMemory.BudgetGB)
			} else {
				fmt.Fprintln(stdout, h.Host)
			}
		}
		mem := ""
		if h.Memory != nil {
			mem = fmt.Sprintf("  %.1f / %.1f GB", h.Memory.UsedGB, h.Memory.BudgetGB)
		}
		fmt.Fprintf(stdout, "  %-8s %-7s%s\n", h.Env, h.State, mem)
		for _, r := range h.Resident {
			s := fmt.Sprintf("    %-14s %s %s %s  %.2f GB", r.Component, r.Model, r.Variant, r.Precision, r.GB)
			if len(r.Loras) > 0 {
				names := make([]string, len(r.Loras))
				for i, l := range r.Loras {
					names[i] = "(hidden)" // another user's own LoRA is redacted
					if l.Name != nil {
						names[i] = *l.Name
					}
				}
				s += "  + " + strings.Join(names, ", ")
			}
			if r.Pinned {
				s += "  pinned"
			}
			if r.LastUsed != "" {
				s += "  used " + r.LastUsed
			}
			fmt.Fprintln(stdout, s)
		}
	}
	return nil
}
