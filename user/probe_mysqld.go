/*
Copyright © 2022 CFC4N <cfc4n.cs@gmail.com>

*/
package user

import (
	"bytes"
	"context"
	"ecapture/assets"
	"github.com/cilium/ebpf"
	manager "github.com/ehids/ebpfmanager"
	"github.com/pkg/errors"
	"golang.org/x/sys/unix"
	"log"
	"math"
	"os"
)

type MMysqldProbe struct {
	Module
	bpfManager        *manager.Manager
	bpfManagerOptions manager.Options
	eventFuncMaps     map[*ebpf.Map]IEventStruct
	eventMaps         []*ebpf.Map
}

//对象初始化
func (this *MMysqldProbe) Init(ctx context.Context, logger *log.Logger, conf IConfig) error {
	this.Module.Init(ctx, logger)
	this.conf = conf
	this.Module.SetChild(this)
	this.eventMaps = make([]*ebpf.Map, 0, 2)
	this.eventFuncMaps = make(map[*ebpf.Map]IEventStruct)
	return nil
}

func (this *MMysqldProbe) Start() error {
	if err := this.start(); err != nil {
		return err
	}
	return nil
}

func (this *MMysqldProbe) start() error {

	// fetch ebpf assets
	byteBuf, err := assets.Asset("user/bytecode/mysqld_kern.o")
	if err != nil {
		return errors.Wrap(err, "couldn't find asset")
	}

	// setup the managers
	err = this.setupManagers()
	if err != nil {
		return errors.Wrap(err, "mysqld module couldn't find binPath.")
	}

	// initialize the bootstrap manager
	if err := this.bpfManager.InitWithOptions(bytes.NewReader(byteBuf), this.bpfManagerOptions); err != nil {
		return errors.Wrap(err, "couldn't init manager")
	}

	// start the bootstrap manager
	if err := this.bpfManager.Start(); err != nil {
		return errors.Wrap(err, "couldn't start bootstrap manager")
	}

	// 加载map信息，map对应events decode表。
	err = this.initDecodeFun()
	if err != nil {
		return err
	}

	return nil
}

func (this *MMysqldProbe) Close() error {
	if err := this.bpfManager.Stop(manager.CleanAll); err != nil {
		return errors.Wrap(err, "couldn't stop manager")
	}
	return nil
}

func (this *MMysqldProbe) setupManagers() error {
	var binaryPath string
	switch this.conf.(*MysqldConfig).elfType {
	case ELF_TYPE_BIN:
		binaryPath = this.conf.(*MysqldConfig).Mysqldpath
	default:
		//如果没找到
		binaryPath = "/usr/sbin/mariadbd"
	}

	_, err := os.Stat(binaryPath)
	if err != nil {
		return err
	}
	attachFunc := this.conf.(*MysqldConfig).FuncName
	offset := this.conf.(*MysqldConfig).Offset
	version := this.conf.(*MysqldConfig).version
	versionInfo := this.conf.(*MysqldConfig).versionInfo

	// mariadbd version : 10.5.13-MariaDB-0ubuntu0.21.04.1
	// objdump -T /usr/sbin/mariadbd |grep dispatch_command
	// 0000000000710410 g    DF .text	0000000000002f35  Base        _Z16dispatch_command19enum_server_commandP3THDPcjbb
	// offset 0x710410
	var probes []*manager.Probe
	switch version {
	case MYSQLD_TYPE_57:
		probes = []*manager.Probe{
			{
				Section:          "uprobe/dispatch_command_57",
				EbpfFuncName:     "mysql57_query",
				AttachToFuncName: attachFunc,
				UprobeOffset:     offset,
				BinaryPath:       binaryPath,
			},
		}
	case MYSQLD_TYPE_80:
		probes = []*manager.Probe{
			{
				Section:          "uprobe/dispatch_command_57", //TODO CHANGE to mysqld80 @CFC4N
				EbpfFuncName:     "mysql57_query",
				AttachToFuncName: attachFunc,
				UprobeOffset:     offset,
				BinaryPath:       binaryPath,
			},
		}
	default:
		probes = []*manager.Probe{
			{
				Section:          "uprobe/dispatch_command",
				EbpfFuncName:     "mysql56_query",
				AttachToFuncName: attachFunc,
				UprobeOffset:     offset,
				BinaryPath:       binaryPath,
			},
		}
	}

	this.bpfManager = &manager.Manager{
		Probes: probes,
		Maps: []*manager.Map{
			{
				Name: "events",
			},
		},
	}

	this.logger.Printf("Mysql Version:%s, binrayPath:%s, FunctionName:%s ,UprobeOffset:%d\n", versionInfo, binaryPath, attachFunc, offset)

	this.bpfManagerOptions = manager.Options{
		DefaultKProbeMaxActive: 512,

		VerifierOptions: ebpf.CollectionOptions{
			Programs: ebpf.ProgramOptions{
				LogSize: 2097152,
			},
		},

		RLimit: &unix.Rlimit{
			Cur: math.MaxUint64,
			Max: math.MaxUint64,
		},
	}
	return nil
}

func (this *MMysqldProbe) DecodeFun(em *ebpf.Map) (IEventStruct, bool) {
	fun, found := this.eventFuncMaps[em]
	return fun, found
}

func (this *MMysqldProbe) initDecodeFun() error {
	// mysqldEventsMap 与解码函数映射
	mysqldEventsMap, found, err := this.bpfManager.GetMap("events")
	if err != nil {
		return err
	}
	if !found {
		return errors.New("cant found map:events")
	}
	this.eventMaps = append(this.eventMaps, mysqldEventsMap)
	this.eventFuncMaps[mysqldEventsMap] = &mysqldEvent{}

	return nil
}

func (this *MMysqldProbe) Events() []*ebpf.Map {
	return this.eventMaps
}

func init() {
	mod := &MMysqldProbe{}
	mod.name = MODULE_NAME_MYSQLD
	mod.mType = PROBE_TYPE_UPROBE
	Register(mod)
}
