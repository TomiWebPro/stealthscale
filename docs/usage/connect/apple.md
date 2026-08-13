# Connecting an Apple client

This documentation has the goal of showing how a user can use the official iOS and macOS [Tailscale](https://tailscale.com) clients with stscale.

!!! info "Instructions on your stscale instance"

    An endpoint with information on how to connect your Apple device
    is also available at `/apple` on your running instance.

## iOS

### Installation

Install the official Tailscale iOS client from the [App Store](https://apps.apple.com/app/tailscale/id1470499037).

### Configuring the stscale URL

- Open the Tailscale app
- Click the account icon in the top-right corner and select `Log in…`.
- Tap the top-right options menu button and select `Use custom coordination server`.
- Enter your instance url (e.g `https://stscale.example.com`)
- Enter your credentials and log in. StealthScale should now be working on your iOS device.

## macOS

### Installation

Choose one of the available [Tailscale clients for macOS](https://tailscale.com/docs/concepts/macos-variants) and
install it.

### Configuring the stscale URL

#### Command line

Use Tailscale's login command to connect with your stscale instance (e.g `https://stscale.example.com`):

```
tailscale login --login-server <YOUR_STSCALE_URL>
```

#### GUI

- Option + Click the Tailscale icon in the menu and hover over the Debug menu
- Under `Custom Login Server`, select `Add Account...`
- Enter the URL of your stscale instance (e.g `https://stscale.example.com`) and press `Add Account`
- Follow the login procedure in the browser

## tvOS

### Installation

Install the official Tailscale tvOS client from the [App Store](https://apps.apple.com/app/tailscale/id1470499037).

!!! danger

    **Don't** open the Tailscale App after installation!

### Configuring the stscale URL

- Open Settings (the Apple tvOS settings) > Apps > Tailscale
- Under `ALTERNATE COORDINATION SERVER URL`, select `URL`
- Enter the URL of your stscale instance (e.g `https://stscale.example.com`) and press `OK`
- Return to the tvOS Home screen
- Open Tailscale
- Click the button `Install VPN configuration` and confirm the appearing popup by clicking the `Allow` button
- Scan the QR code and follow the login procedure
