package obsd

import (
	"context"
	"sort"
	"strings"
)

// Detailansichten für Datastores und Portgruppen mit „Verwendet von“.

type DatastoreUse struct {
	VM    string   `json:"vm"`
	State string   `json:"state"`
	Disks []VMDisk `json:"disks"`
	Bytes int64    `json:"bytes"` // belegt durch die Images dieser VM
}

type DatastoreDetail struct {
	DatastoreInfo
	Disk    *SANDisk       `json:"disk,omitempty"`
	Targets []*ISCSITarget `json:"targets"` // konfigurierte iSCSI-Ziele (die Zuordnung zur Disk kennt OpenBSD nicht)
	Users   []DatastoreUse `json:"users"`
	Images  []VMImage      `json:"images"`
	Hidden  int            `json:"hidden"` // wegen Eingrenzung ausgeblendete VMs
}

func (h *Host) vmStates(ctx context.Context) map[string]string {
	m := map[string]string{}
	if st, err := h.vmctlStatus(ctx); err == nil {
		for _, s := range st {
			m[s.Name] = s.State
		}
	}
	return m
}

// visible filtert VM-Namen nach der Eingrenzung (nil = alles) und zählt die ausgeblendeten.
func visible(vm string, scope []string) bool { return scope == nil || contains(scope, vm) }

func (h *Host) DatastoreDetail(ctx context.Context, name string, scope []string) (*DatastoreDetail, error) {
	o, err := h.SAN(ctx)
	if err != nil {
		return nil, err
	}
	var di *DatastoreInfo
	for i := range o.Datastores {
		if o.Datastores[i].Name == name {
			di = &o.Datastores[i]
		}
	}
	if di == nil {
		return nil, invalid("Datastore %q gibt es nicht", name)
	}
	d := &DatastoreDetail{DatastoreInfo: *di, Targets: o.Targets, Users: []DatastoreUse{}, Images: []VMImage{}}
	for i := range o.Disks {
		if o.Disks[i].DUID == di.DUID && o.Disks[i].Labeled {
			d.Disk = &o.Disks[i]
		}
	}
	conf, err := h.loadVMConf()
	if err != nil {
		return nil, err
	}
	states := h.vmStates(ctx)
	used := map[string][]string{}
	prefix := strings.TrimRight(di.Mount, "/") + "/"
	for _, v := range conf.VMs {
		var disks []VMDisk
		var total int64
		for _, p := range append(append([]string{}, v.Disks...), v.CDROM) {
			if p == "" || !strings.HasPrefix(p, prefix) {
				continue
			}
			used[p] = append(used[p], v.Name)
			dk := h.diskInfo(p)
			disks = append(disks, dk)
			total += dk.Size
		}
		if len(disks) == 0 {
			continue
		}
		if !visible(v.Name, scope) {
			d.Hidden++
			continue
		}
		st := states[v.Name]
		if st == "" {
			st = "stopped"
		}
		d.Users = append(d.Users, DatastoreUse{VM: v.Name, State: st, Disks: disks, Bytes: total})
	}
	sort.Slice(d.Users, func(i, j int) bool { return d.Users[i].VM < d.Users[j].VM })
	d.Images = h.listImages(di.Mount, []string{".qcow2", ".img", ".raw", ".iso", ".fs"}, used)
	if scope != nil { // fremde VMs in UsedBy nicht verraten
		for i := range d.Images {
			keep := []string{}
			for _, u := range d.Images[i].UsedBy {
				if contains(scope, u) {
					keep = append(keep, u)
				}
			}
			if len(keep) != len(d.Images[i].UsedBy) {
				keep = append(keep, "(andere VM)")
			}
			d.Images[i].UsedBy = keep
		}
	}
	return d, nil
}

type PortGroupUse struct {
	VM     string `json:"vm"`
	State  string `json:"state"`
	Index  int    `json:"index"`
	LLAddr string `json:"lladdr"`
	IP     string `json:"ip,omitempty"`
	FQDN   string `json:"fqdn,omitempty"`
	In     string `json:"in"`
	Out    string `json:"out"`
	Rules  int    `json:"rules"`
}

type PortGroupDetail struct {
	PortGroupInfo
	Uses     []PortGroupUse    `json:"uses"`
	Uplink   *UplinkInfo       `json:"uplinkInfo,omitempty"`
	Files    map[string]string `json:"files"`
	Switch   string            `json:"switchBlock"`
	Reserved int               `json:"reserved"` // feste Adressen
	Hidden   int               `json:"hidden"`
}

func (h *Host) PortGroupDetail(ctx context.Context, name string, scope []string) (*PortGroupDetail, error) {
	o, err := h.VNet(ctx)
	if err != nil {
		return nil, err
	}
	var pi *PortGroupInfo
	for i := range o.PortGroups {
		if o.PortGroups[i].Name == name {
			pi = &o.PortGroups[i]
		}
	}
	if pi == nil {
		return nil, invalid("Portgruppe %q gibt es nicht", name)
	}
	d := &PortGroupDetail{PortGroupInfo: *pi, Uses: []PortGroupUse{}, Files: pi.hostnameFiles(), Switch: pi.switchBlock()}
	states := h.vmStates(ctx)
	for _, u := range o.Uplinks {
		if u.Name == pi.Uplink {
			uu := u
			d.Uplink = &uu
		}
	}
	for _, n := range o.NICs {
		if n.Switch != name {
			continue
		}
		if n.IP != "" {
			d.Reserved++
		}
		if !visible(n.VM, scope) {
			d.Hidden++
			continue
		}
		st := states[n.VM]
		if st == "" {
			st = "stopped"
		}
		p := o.Policies[n.VM]
		d.Uses = append(d.Uses, PortGroupUse{VM: n.VM, State: st, Index: n.Index, LLAddr: n.LLAddr, IP: n.IP, FQDN: n.FQDN, In: p.In, Out: p.Out, Rules: len(o.Rules[n.VM])})
	}
	sort.Slice(d.Uses, func(i, j int) bool {
		if d.Uses[i].VM != d.Uses[j].VM {
			return d.Uses[i].VM < d.Uses[j].VM
		}
		return d.Uses[i].Index < d.Uses[j].Index
	})
	if scope != nil { // VM-Namen der Eingrenzung: nur die eigenen aufführen
		d.PortGroupInfo.VMs = []string{}
		for _, u := range d.Uses {
			if !contains(d.PortGroupInfo.VMs, u.VM) {
				d.PortGroupInfo.VMs = append(d.PortGroupInfo.VMs, u.VM)
			}
		}
	}
	return d, nil
}
