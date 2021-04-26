package keeper

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/ethereum/go-ethereum/common"

	"github.com/cosmos/gravity-bridge/module/x/gravity/types"
)

// AttestationHandler defines an interface that processes incoming attestations
// from Ethereum. While the default handler only mints ERC20 tokens, additional
// custom functionality can be implemented by passing an external handler to the
// bridge keeper.
//
// Examples of custom functionality could be, but not limited to:
//
// - Transfering newly minted ERC20 tokens (represented as an sdk.Coins) to a
// given recipient, either local or via IBC to a counterparty chain
//
// - Pooling the tokens into an escrow account for interest accruing DeFi solutions
//
// - Deposit into an AMM pair
type AttestationHandler interface {
	OnAttestation(ctx sdk.Context, attestation types.Attestation) error
}

// DefaultAttestationHandler is the default handler for processing observed
// event attestations received from Ethereum.
type DefaultAttestationHandler struct {
	keeper Keeper
}

var _ AttestationHandler = DefaultAttestationHandler{}

// OnAttestation processes ethereum event upon attestation and performs a custom
// logic.
//
// TODO: clean up
func (handler DefaultAttestationHandler) OnAttestation(ctx sdk.Context, attestation types.Attestation) error {
	// FIXME: create func
	event, found := handler.keeper.GetEthEvent(ctx, attestation.EventID)
	if !found {
		// TODO: err msg
		return fmt.Errorf("not found")
	}

	switch event := event.(type) {
	case *types.DepositEvent:
		// Check if coin is Cosmos-originated asset and get denom
		isCosmosOriginated, denom := a.keeper.ERC20ToDenomLookup(ctx, event.TokenContract)

		if isCosmosOriginated {
			// If it is cosmos originated, unlock the coins
			coins := sdk.Coins{sdk.NewCoin(denom, event.Amount)}

			addr, err := sdk.AccAddressFromBech32(event.CosmosReceiver)
			if err != nil {
				return sdkerrors.Wrap(err, "invalid reciever address")
			}

			if err = a.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleName, addr, coins); err != nil {
				return sdkerrors.Wrap(err, "cosmos coins")
			}
		} else {
			// If it is not cosmos originated, mint the coins (aka vouchers)
			coins := sdk.Coins{sdk.NewCoin(denom, event.Amount)}

			if err := a.bankKeeper.MintCoins(ctx, types.ModuleName, coins); err != nil {
				return sdkerrors.Wrapf(err, "mint vouchers coins: %s", coins)
			}

			addr, err := sdk.AccAddressFromBech32(event.CosmosReceiver)
			if err != nil {
				return sdkerrors.Wrap(err, "invalid receiver address")
			}

			if err = a.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleName, addr, coins); err != nil {
				return sdkerrors.Wrap(err, "erc20 vouchers")
			}
		}
	case *types.WithdrawEvent:
		a.keeper.OutgoingTxBatchExecuted(ctx, event.TokenContract, event.BatchNonce)
	case *types.ERC20DeployedEvent:
		return RegisterERC20(a.keeper, ctx, event)
	default:
		return sdkerrors.Wrapf(types.ErrInvalid, "unsupported event type %s: %T", event.GetType(), event)
	}

	return nil
}

// RegisterERC20
func RegisterERC20(k Keeper, ctx sdk.Context, event types.CosmosERC20DeployedEvent) error {
	// Check if it already exists
	contractAddr, found := k.GetERC20ContractFromCoinDenom(ctx, event.CosmosDenom)
	if found {
		return sdkerrors.Wrap(
			// TODO: fix
			types.ErrContractNotFound,
			fmt.Sprintf("erc20 contract %s already registered for coin denom %s", contractAddr.String(), event.CosmosDenom))
	}

	// Check if denom exists
	metadata := k.bankKeeper.GetDenomMetaData(ctx, event.CosmosDenom)

	// NOTE: this will fail on all IBC vouchers or any Cosmos coin that hasn't
	// a denom metadata value defined
	// TODO: discuss if we should create/set a new metadata if it's not currently
	// set to store for the given cosmos denom
	if err := validateCoinMetadata(event, metadata); err != nil {
		return err
	}

	tokenContract := common.HexToAddress(event.TokenContract)
	k.setERC20DenomMap(ctx, event.CosmosDenom, tokenContract)

	k.Logger(ctx).Debug("erc20 token registered", "contract-address", event.TokenContract, "cosmos-denom", event.CosmosDenom)
	return nil
}

// validateCoinMetadata performs a stateless validation on the metadata fields and compares its values
// with the deployed ERC20 contract values.
func validateCoinMetadata(event types.CosmosERC20DeployedEvent, metadata banktypes.Metadata) error {
	if err := metadata.Validate(); err != nil {
		return err
	}

	// Check if attributes of ERC20 match Cosmos denom
	if event.Name != metadata.Display {
		return sdkerrors.Wrapf(
			// TODO: fix
			types.ErrContractNotFound,
			"ERC20 name %s does not match denom display %s", event.Name, metadata.Description)
	}

	if event.Symbol != metadata.Display {
		return sdkerrors.Wrapf(
			// TODO: fix
			types.ErrContractNotFound,
			"ERC20 symbol %s does not match denom display %s", event.Symbol, metadata.Display)
	}

	// NOTE: denomination units can't be empty and are sorted in ASC order
	decimals := metadata.DenomUnits[len(metadata.DenomUnits)-1].Exponent

	if decimals != uint32(event.Decimals) {
		return sdkerrors.Wrapf(
			// TODO: fix
			types.ErrContractNotFound,
			"ERC20 decimals %d does not match denom decimals %d", event.Decimals, decimals)
	}

	return nil
}