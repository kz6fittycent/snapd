// -*- Mode: Go; indent-tabs-mode: t -*-

/*
 * Copyright (C) 2020 Canonical Ltd
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License version 3 as
 * published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 *
 */

package boot

import (
	"fmt"
	"path/filepath"

	"github.com/snapcore/snapd/asserts"
	"github.com/snapcore/snapd/bootloader"
	"github.com/snapcore/snapd/dirs"
	"github.com/snapcore/snapd/secboot"
	"github.com/snapcore/snapd/seed"
	"github.com/snapcore/snapd/snap"
	"github.com/snapcore/snapd/strutil"
	"github.com/snapcore/snapd/timings"
)

var (
	secbootSealKey   = secboot.SealKey
	secbootResealKey = secboot.ResealKey

	seedReadSystemEssential = seed.ReadSystemEssential
)

func bootChainsFileUnder(rootdir string) string {
	return filepath.Join(dirs.SnapFDEDirUnder(rootdir), "boot-chains")
}

// sealKeyToModeenv seals the supplied key to the parameters specified
// in modeenv.
func sealKeyToModeenv(key secboot.EncryptionKey, model *asserts.Model, modeenv *Modeenv) error {
	// build the recovery mode boot chain
	rbl, err := bootloader.Find(InitramfsUbuntuSeedDir, &bootloader.Options{
		Role: bootloader.RoleRecovery,
	})
	if err != nil {
		return fmt.Errorf("cannot find the recovery bootloader: %v", err)
	}

	recoveryBootChains, err := recoveryBootChainsForSystems([]string{modeenv.RecoverySystem}, rbl, model, modeenv)
	if err != nil {
		return fmt.Errorf("cannot compose recovery boot chains: %v", err)
	}

	// build the run mode boot chains
	bl, err := bootloader.Find(InitramfsUbuntuBootDir, &bootloader.Options{
		Role:        bootloader.RoleRunMode,
		NoSlashBoot: true,
	})
	if err != nil {
		return fmt.Errorf("cannot find the bootloader: %v", err)
	}
	cmdline, err := ComposeCandidateCommandLine(model)
	if err != nil {
		return fmt.Errorf("cannot compose the candidate command line: %v", err)
	}

	runModeBootChains, err := runModeBootChains(rbl, bl, model, modeenv, cmdline)
	if err != nil {
		return fmt.Errorf("cannot compose run mode boot chains: %v", err)
	}

	pbc := toPredictableBootChains(append(runModeBootChains, recoveryBootChains...))

	roleToBlName := map[bootloader.Role]string{
		bootloader.RoleRecovery: rbl.Name(),
		bootloader.RoleRunMode:  bl.Name(),
	}

	// get model parameters from bootchains
	modelParams, err := sealKeyModelParams(pbc, roleToBlName)
	if err != nil {
		return fmt.Errorf("cannot prepare for key sealing: %v", err)
	}
	sealKeyParams := &secboot.SealKeyParams{
		ModelParams:             modelParams,
		KeyFile:                 filepath.Join(InitramfsEncryptionKeyDir, "ubuntu-data.sealed-key"),
		TPMPolicyUpdateDataFile: filepath.Join(InstallHostFDEDataDir, "policy-update-data"),
		TPMLockoutAuthFile:      filepath.Join(InstallHostFDEDataDir, "tpm-lockout-auth"),
	}
	// finally, seal the key
	if err := secbootSealKey(key, sealKeyParams); err != nil {
		return fmt.Errorf("cannot seal the encryption key: %v", err)
	}

	installBootChainsPath := bootChainsFileUnder(InstallHostWritableDir)
	if err := writeBootChains(pbc, installBootChainsPath); err != nil {
		return err
	}

	return nil
}

// resealKeyToModeenv reseals the existing encryption key to the
// parameters specified in modeenv.
func resealKeyToModeenv(model *asserts.Model, modeenv *Modeenv) error {
	// TODO:UC20: should we use Initramfs*Dirs in run mode
	// both in terms of var name and mount points?
	// should we use dir(mode) functions at least?
	// see similar comment in SetRecoveryBootSystemAndMode

	// build the recovery mode boot chain
	rbl, err := bootloader.Find(InitramfsUbuntuSeedDir, &bootloader.Options{
		Role: bootloader.RoleRecovery,
	})
	if err != nil {
		return fmt.Errorf("cannot find the recovery bootloader: %v", err)
	}

	recoveryBootChains, err := recoveryBootChainsForSystems(modeenv.CurrentRecoverySystems, rbl, model, modeenv)
	if err != nil {
		return fmt.Errorf("cannot compose recovery boot chains: %v", err)
	}

	// build the run mode boot chains
	bl, err := bootloader.Find(InitramfsUbuntuBootDir, &bootloader.Options{
		Role:        bootloader.RoleRunMode,
		NoSlashBoot: true,
	})
	if err != nil {
		return fmt.Errorf("cannot find the bootloader: %v", err)
	}
	cmdline, err := ComposeCommandLine(model)
	if err != nil {
		return fmt.Errorf("cannot compose the run mode command line: %v", err)
	}

	runModeBootChains, err := runModeBootChains(rbl, bl, model, modeenv, cmdline)
	if err != nil {
		return fmt.Errorf("cannot compose run mode boot chains: %v", err)
	}

	pbc := toPredictableBootChains(append(runModeBootChains, recoveryBootChains...))

	// TODO:UC20: load and compare the predictable bootchains

	roleToBlName := map[bootloader.Role]string{
		bootloader.RoleRecovery: rbl.Name(),
		bootloader.RoleRunMode:  bl.Name(),
	}

	// get model parameters from bootchains
	modelParams, err := sealKeyModelParams(pbc, roleToBlName)
	if err != nil {
		return fmt.Errorf("cannot prepare for key resealing: %v", err)
	}
	resealKeyParams := &secboot.ResealKeyParams{
		ModelParams:             modelParams,
		KeyFile:                 filepath.Join(InitramfsEncryptionKeyDir, "ubuntu-data.sealed-key"),
		TPMPolicyUpdateDataFile: filepath.Join(InstallHostFDEDataDir, "policy-update-data"),
	}
	if err := secbootResealKey(resealKeyParams); err != nil {
		return fmt.Errorf("cannot reseal the encryption key: %v", err)
	}

	bootChainsPath := bootChainsFileUnder(InstallHostWritableDir)
	if err := writeBootChains(pbc, bootChainsPath); err != nil {
		return err
	}

	return nil
}

func recoveryBootChainsForSystems(systems []string, rbl bootloader.Bootloader, model *asserts.Model, modeenv *Modeenv) (chains []bootChain, err error) {
	tbl, ok := rbl.(bootloader.TrustedAssetsBootloader)
	if !ok {
		return nil, fmt.Errorf("bootloader doesn't support trusted assets")
	}

	for _, system := range systems {
		// get the command line
		cmdline, err := ComposeRecoveryCommandLine(model, system)
		if err != nil {
			return nil, fmt.Errorf("cannot obtain recovery kernel command line: %v", err)
		}

		// get kernel information from seed
		perf := timings.New(nil)
		_, snaps, err := seedReadSystemEssential(dirs.SnapSeedDir, system, []snap.Type{snap.TypeKernel}, perf)
		if err != nil {
			return nil, err
		}
		if len(snaps) != 1 {
			return nil, fmt.Errorf("cannot obtain recovery kernel snap")
		}
		seedKernel := snaps[0]

		var kernelRev string
		if seedKernel.SideInfo.Revision.Store() {
			kernelRev = seedKernel.SideInfo.Revision.String()
		}

		recoveryBootChain, err := tbl.RecoveryBootChain(seedKernel.Path)
		if err != nil {
			return nil, err
		}

		// get asset chains
		assetChain, kbf, err := buildBootAssets(recoveryBootChain, modeenv)
		if err != nil {
			return nil, err
		}

		chains = append(chains, bootChain{
			BrandID:        model.BrandID(),
			Model:          model.Model(),
			Grade:          model.Grade(),
			ModelSignKeyID: model.SignKeyID(),
			AssetChain:     assetChain,
			Kernel:         seedKernel.SnapName(),
			KernelRevision: kernelRev,
			KernelCmdlines: []string{cmdline},
			model:          model,
			kernelBootFile: kbf,
		})
	}
	return chains, nil
}

func runModeBootChains(rbl, bl bootloader.Bootloader, model *asserts.Model, modeenv *Modeenv, cmdline string) ([]bootChain, error) {
	tbl, ok := rbl.(bootloader.TrustedAssetsBootloader)
	if !ok {
		return nil, fmt.Errorf("recovery bootloader doesn't support trusted assets")
	}

	chains := make([]bootChain, 0, len(modeenv.CurrentKernels))
	for _, k := range modeenv.CurrentKernels {
		info, err := snap.ParsePlaceInfoFromSnapFileName(k)
		if err != nil {
			return nil, err
		}
		runModeBootChain, err := tbl.BootChain(bl, info.MountFile())
		if err != nil {
			return nil, err
		}

		// get asset chains
		assetChain, kbf, err := buildBootAssets(runModeBootChain, modeenv)
		if err != nil {
			return nil, err
		}
		var kernelRev string
		if info.SnapRevision().Store() {
			kernelRev = info.SnapRevision().String()
		}
		chains = append(chains, bootChain{
			BrandID:        model.BrandID(),
			Model:          model.Model(),
			Grade:          model.Grade(),
			ModelSignKeyID: model.SignKeyID(),
			AssetChain:     assetChain,
			Kernel:         info.SnapName(),
			KernelRevision: kernelRev,
			KernelCmdlines: []string{cmdline},
			model:          model,
			kernelBootFile: kbf,
		})
	}
	return chains, nil
}

// buildBootAssets takes the BootFiles of a bootloader boot chain and
// produces corresponding bootAssets with the matching current asset
// hashes from modeenv plus it returns separately the last BootFile
// which is for the kernel.
func buildBootAssets(bootFiles []bootloader.BootFile, modeenv *Modeenv) (assets []bootAsset, kernel bootloader.BootFile, err error) {
	assets = make([]bootAsset, len(bootFiles)-1)

	// the last element is the kernel which is not a boot asset
	for i, bf := range bootFiles[:len(bootFiles)-1] {
		name := filepath.Base(bf.Path)
		var hashes []string
		var ok bool
		if bf.Role == bootloader.RoleRecovery {
			hashes, ok = modeenv.CurrentTrustedRecoveryBootAssets[name]
		} else {
			hashes, ok = modeenv.CurrentTrustedBootAssets[name]
		}
		if !ok {
			return nil, kernel, fmt.Errorf("cannot find expected boot asset %s in modeenv", name)
		}
		assets[i] = bootAsset{
			Role:   bf.Role,
			Name:   name,
			Hashes: hashes,
		}
	}

	return assets, bootFiles[len(bootFiles)-1], nil
}

func sealKeyModelParams(pbc predictableBootChains, roleToBlName map[bootloader.Role]string) ([]*secboot.SealKeyModelParams, error) {
	modelToParams := map[*asserts.Model]*secboot.SealKeyModelParams{}
	modelParams := make([]*secboot.SealKeyModelParams, 0, len(pbc))

	for _, bc := range pbc {
		loadChains, err := bootAssetsToLoadChains(bc.AssetChain, bc.kernelBootFile, roleToBlName)
		if err != nil {
			return nil, fmt.Errorf("cannot build load chains with current boot assets: %s", err)
		}

		// group parameters by model, reuse an existing SealKeyModelParams
		// if the model is the same.
		if params, ok := modelToParams[bc.model]; ok {
			params.KernelCmdlines = strutil.SortedListsUniqueMerge(params.KernelCmdlines, bc.KernelCmdlines)
			params.EFILoadChains = append(params.EFILoadChains, loadChains...)
		} else {
			param := &secboot.SealKeyModelParams{
				Model:          bc.model,
				KernelCmdlines: bc.KernelCmdlines,
				EFILoadChains:  loadChains,
			}
			modelParams = append(modelParams, param)
			modelToParams[bc.model] = param
		}
	}

	return modelParams, nil
}

// isResealNeeded returns true when the predictable boot chains provided as
// input do not match the cached boot chains on disk under rootdir.
func isResealNeeded(pbc predictableBootChains, rootdir string) (bool, error) {
	previousPbc, err := readBootChains(bootChainsFileUnder(rootdir))
	if err != nil {
		return false, err
	}
	return !predictableBootChainsEqualForReseal(pbc, previousPbc), nil
}
