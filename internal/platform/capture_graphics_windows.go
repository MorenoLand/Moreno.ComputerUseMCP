//go:build windows && amd64

package platform

import (
	"context"
	"errors"
	"fmt"
	"image"
	"runtime"
	"time"
	"unsafe"

	"github.com/MorenoLand/Moreno.ComputerUseMCP/internal/computer"
	win32runtime "github.com/deploymenttheory/go-bindings-win32/bindings/runtime/win32"
	graphicsdirect3d "github.com/deploymenttheory/go-bindings-win32/bindings/win32/graphics/direct3d"
	graphicsdirect3d11 "github.com/deploymenttheory/go-bindings-win32/bindings/win32/graphics/direct3d11"
	graphicsdxgi "github.com/deploymenttheory/go-bindings-win32/bindings/win32/graphics/dxgi"
	graphicsdxgicommon "github.com/deploymenttheory/go-bindings-win32/bindings/win32/graphics/dxgi/common"
	systemwinrt "github.com/deploymenttheory/go-bindings-win32/bindings/win32/system/winrt"
	winrtinterop "github.com/deploymenttheory/go-bindings-win32/bindings/win32/system/winrt/direct3d11"
	"github.com/deploymenttheory/go-bindings-winrt/bindings/runtime/winrt"
	"github.com/deploymenttheory/go-bindings-winrt/bindings/winrt/foundation"
	capturepkg "github.com/deploymenttheory/go-bindings-winrt/bindings/winrt/graphics/capture"
	graphicsdirectx "github.com/deploymenttheory/go-bindings-winrt/bindings/winrt/graphics/directx"
	graphicsdirectxdirect3d11 "github.com/deploymenttheory/go-bindings-winrt/bindings/winrt/graphics/directx/direct3d11"
	"github.com/deploymenttheory/go-bindings-winrt/bindings/winrt/ui"
)

func captureWindowGraphics(ctx context.Context, hwnd uintptr, options computer.ScreenshotOptions) ([]byte, string, int, int, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ctx.Err(); err != nil {
		return nil, "", 0, 0, err
	}
	if err := winrt.Initialize(); err != nil {
		return nil, "", 0, 0, fmt.Errorf("initialize Windows Runtime: %w", err)
	}
	itemStatics, err := capturepkg.GraphicsCaptureItemStatics2()
	if err != nil {
		return nil, "", 0, 0, fmt.Errorf("get graphics capture item factory: %w", err)
	}
	defer releaseCOM(unsafe.Pointer(itemStatics))
	item, err := itemStatics.TryCreateFromWindowId(ui.WindowId{Value: uint64(hwnd)})
	if err != nil || item == nil {
		if err == nil {
			err = errors.New("returned no capture item")
		}
		return nil, "", 0, 0, fmt.Errorf("create graphics capture item: %w", err)
	}
	defer releaseCOM(unsafe.Pointer(item))
	size, err := item.Size()
	if err != nil {
		return nil, "", 0, 0, fmt.Errorf("get graphics capture size: %w", err)
	}
	if size.Width <= 0 || size.Height <= 0 {
		return nil, "", 0, 0, errors.New("graphics capture item has no area")
	}
	d3dDevice, deviceContext, err := createCaptureDevice()
	if err != nil {
		return nil, "", 0, 0, err
	}
	defer releaseCOM(unsafe.Pointer(deviceContext))
	defer releaseCOM(unsafe.Pointer(d3dDevice))
	var dxgiUnknown *win32runtime.IUnknown
	if err := (*win32runtime.IUnknown)(unsafe.Pointer(d3dDevice)).QueryInterface(&graphicsdxgi.IID_IDXGIDevice, &dxgiUnknown); err != nil {
		return nil, "", 0, 0, fmt.Errorf("query DXGI device: %w", err)
	}
	dxgiDevice := (*graphicsdxgi.IDXGIDevice)(unsafe.Pointer(dxgiUnknown))
	defer releaseCOM(unsafe.Pointer(dxgiDevice))
	var graphicsDevice *systemwinrt.IInspectable
	if err := winrtinterop.CreateDirect3D11DeviceFromDXGIDevice(dxgiDevice, &graphicsDevice); err != nil {
		return nil, "", 0, 0, fmt.Errorf("create WinRT Direct3D device: %w", err)
	}
	defer releaseCOM(unsafe.Pointer(graphicsDevice))
	winrtDevice := (*graphicsdirectxdirect3d11.IDirect3DDevice)(unsafe.Pointer(graphicsDevice))
	poolStatics, err := capturepkg.Direct3D11CaptureFramePoolStatics2()
	if err != nil {
		return nil, "", 0, 0, fmt.Errorf("get capture frame pool factory: %w", err)
	}
	defer releaseCOM(unsafe.Pointer(poolStatics))
	pool, err := poolStatics.CreateFreeThreaded(winrtDevice, graphicsdirectx.DirectXPixelFormatB8G8R8A8UIntNormalized, 1, size)
	if err != nil || pool == nil {
		if err == nil {
			err = errors.New("returned no capture frame pool")
		}
		return nil, "", 0, 0, fmt.Errorf("create capture frame pool: %w", err)
	}
	defer releaseCOM(unsafe.Pointer(pool))
	defer closeWinRT(unsafe.Pointer(pool))
	session, err := pool.CreateCaptureSession(item)
	if err != nil || session == nil {
		if err == nil {
			err = errors.New("returned no capture session")
		}
		return nil, "", 0, 0, fmt.Errorf("create capture session: %w", err)
	}
	defer releaseCOM(unsafe.Pointer(session))
	defer closeWinRT(unsafe.Pointer(session))
	session2, err := winrt.QueryInterface[capturepkg.IGraphicsCaptureSession2](unsafe.Pointer(session), &capturepkg.IID_IGraphicsCaptureSession2)
	if err != nil {
		return nil, "", 0, 0, fmt.Errorf("query capture session options: %w", err)
	}
	if err := session2.SetIsCursorCaptureEnabled(options.IncludeCursor); err != nil {
		releaseCOM(unsafe.Pointer(session2))
		return nil, "", 0, 0, fmt.Errorf("set cursor capture: %w", err)
	}
	releaseCOM(unsafe.Pointer(session2))
	if session3, err := winrt.QueryInterface[capturepkg.IGraphicsCaptureSession3](unsafe.Pointer(session), &capturepkg.IID_IGraphicsCaptureSession3); err == nil {
		_ = session3.SetIsBorderRequired(false)
		releaseCOM(unsafe.Pointer(session3))
	}
	if err := session.StartCapture(); err != nil {
		return nil, "", 0, 0, fmt.Errorf("start capture: %w", err)
	}
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, "", 0, 0, err
		}
		frame, frameErr := pool.TryGetNextFrame()
		if frameErr == nil && frame != nil {
			defer releaseCOM(unsafe.Pointer(frame))
			return copyCaptureFrame(frame, d3dDevice, deviceContext, options)
		}
		if frameErr != nil && !isCapturePending(frameErr) {
			return nil, "", 0, 0, fmt.Errorf("get capture frame: %w", frameErr)
		}
		select {
		case <-ctx.Done():
			return nil, "", 0, 0, ctx.Err()
		case <-deadline.C:
			return nil, "", 0, 0, errors.New("timed out waiting for graphics capture frame")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

func createCaptureDevice() (*graphicsdirect3d11.ID3D11Device, *graphicsdirect3d11.ID3D11DeviceContext, error) {
	var device *graphicsdirect3d11.ID3D11Device
	var deviceContext *graphicsdirect3d11.ID3D11DeviceContext
	var featureLevel graphicsdirect3d.D3D_FEATURE_LEVEL
	flags := graphicsdirect3d11.D3D11_CREATE_DEVICE_BGRA_SUPPORT
	levels := []graphicsdirect3d.D3D_FEATURE_LEVEL{graphicsdirect3d.D3D_FEATURE_LEVEL_11_1, graphicsdirect3d.D3D_FEATURE_LEVEL_11_0}
	err := graphicsdirect3d11.D3D11CreateDevice(nil, graphicsdirect3d.D3D_DRIVER_TYPE_HARDWARE, 0, flags, levels, 7, &device, &featureLevel, &deviceContext)
	if err != nil {
		releaseCOM(unsafe.Pointer(deviceContext))
		releaseCOM(unsafe.Pointer(device))
		device = nil
		deviceContext = nil
		err = graphicsdirect3d11.D3D11CreateDevice(nil, graphicsdirect3d.D3D_DRIVER_TYPE_HARDWARE, 0, flags, []graphicsdirect3d.D3D_FEATURE_LEVEL{graphicsdirect3d.D3D_FEATURE_LEVEL_11_0}, 7, &device, &featureLevel, &deviceContext)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("create Direct3D device: %w", err)
	}
	return device, deviceContext, nil
}

func copyCaptureFrame(frame *capturepkg.IDirect3D11CaptureFrame, device *graphicsdirect3d11.ID3D11Device, deviceContext *graphicsdirect3d11.ID3D11DeviceContext, options computer.ScreenshotOptions) ([]byte, string, int, int, error) {
	surface, err := frame.Surface()
	if err != nil || surface == nil {
		if err == nil {
			err = errors.New("returned no frame surface")
		}
		return nil, "", 0, 0, fmt.Errorf("get frame surface: %w", err)
	}
	defer releaseCOM(unsafe.Pointer(surface))
	access, err := winrt.QueryInterface[winrtinterop.IDirect3DDxgiInterfaceAccess](unsafe.Pointer(surface), &winrtinterop.IID_IDirect3DDxgiInterfaceAccess)
	if err != nil {
		return nil, "", 0, 0, fmt.Errorf("query frame surface access: %w", err)
	}
	defer releaseCOM(unsafe.Pointer(access))
	var textureUnknown *win32runtime.IUnknown
	if err := access.GetInterface(&graphicsdirect3d11.IID_ID3D11Texture2D, &textureUnknown); err != nil {
		return nil, "", 0, 0, fmt.Errorf("get frame texture: %w", err)
	}
	texture := (*graphicsdirect3d11.ID3D11Texture2D)(unsafe.Pointer(textureUnknown))
	defer releaseCOM(unsafe.Pointer(texture))
	var description graphicsdirect3d11.D3D11_TEXTURE2D_DESC
	texture.GetDesc(&description)
	if description.Width == 0 || description.Height == 0 {
		return nil, "", 0, 0, errors.New("frame texture has no area")
	}
	if description.SampleDesc.Count != 1 || description.ArraySize != 1 || description.MipLevels != 1 {
		return nil, "", 0, 0, errors.New("frame texture has unsupported layout")
	}
	bgra := description.Format == graphicsdxgicommon.DXGI_FORMAT_B8G8R8A8_UNORM || description.Format == graphicsdxgicommon.DXGI_FORMAT_B8G8R8A8_UNORM_SRGB
	rgba := description.Format == graphicsdxgicommon.DXGI_FORMAT_R8G8B8A8_UNORM || description.Format == graphicsdxgicommon.DXGI_FORMAT_R8G8B8A8_UNORM_SRGB
	if !bgra && !rgba {
		return nil, "", 0, 0, fmt.Errorf("frame texture has unsupported format %v", description.Format)
	}
	stagingDescription := description
	stagingDescription.MipLevels = 1
	stagingDescription.ArraySize = 1
	stagingDescription.SampleDesc.Count = 1
	stagingDescription.SampleDesc.Quality = 0
	stagingDescription.Usage = graphicsdirect3d11.D3D11_USAGE_STAGING
	stagingDescription.BindFlags = 0
	stagingDescription.CPUAccessFlags = uint32(graphicsdirect3d11.D3D11_CPU_ACCESS_READ)
	stagingDescription.MiscFlags = 0
	var staging *graphicsdirect3d11.ID3D11Texture2D
	if err := device.CreateTexture2D(&stagingDescription, nil, &staging); err != nil {
		return nil, "", 0, 0, fmt.Errorf("create staging texture: %w", err)
	}
	defer releaseCOM(unsafe.Pointer(staging))
	sourceResource := (*graphicsdirect3d11.ID3D11Resource)(unsafe.Pointer(texture))
	destinationResource := (*graphicsdirect3d11.ID3D11Resource)(unsafe.Pointer(staging))
	deviceContext.CopyResource(destinationResource, sourceResource)
	deviceContext.Flush()
	var mapped graphicsdirect3d11.D3D11_MAPPED_SUBRESOURCE
	if err := deviceContext.Map(destinationResource, 0, graphicsdirect3d11.D3D11_MAP_READ, 0, &mapped); err != nil {
		return nil, "", 0, 0, fmt.Errorf("map staging texture: %w", err)
	}
	defer deviceContext.Unmap(destinationResource, 0)
	rowBytes := int(description.Width) * 4
	if mapped.PData == nil || int(mapped.RowPitch) < rowBytes {
		return nil, "", 0, 0, errors.New("mapped frame texture has an invalid row pitch")
	}
	img := image.NewRGBA(image.Rect(0, 0, int(description.Width), int(description.Height)))
	for y := 0; y < int(description.Height); y++ {
		source := unsafe.Slice((*byte)(unsafe.Add(mapped.PData, uintptr(y)*uintptr(mapped.RowPitch))), rowBytes)
		for x := 0; x < int(description.Width); x++ {
			sourceOffset := x * 4
			targetOffset := y*img.Stride + sourceOffset
			if bgra {
				img.Pix[targetOffset] = source[sourceOffset+2]
				img.Pix[targetOffset+1] = source[sourceOffset+1]
				img.Pix[targetOffset+2] = source[sourceOffset]
			} else {
				img.Pix[targetOffset] = source[sourceOffset]
				img.Pix[targetOffset+1] = source[sourceOffset+1]
				img.Pix[targetOffset+2] = source[sourceOffset+2]
			}
			img.Pix[targetOffset+3] = 0xff
		}
	}
	return computer.EncodeImage(img, options)
}

func closeWinRT(value unsafe.Pointer) {
	if value == nil {
		return
	}
	closable, err := winrt.QueryInterface[foundation.IClosable](value, &foundation.IID_IClosable)
	if err != nil {
		return
	}
	_ = closable.Close()
	releaseCOM(unsafe.Pointer(closable))
}

func releaseCOM(value unsafe.Pointer) {
	if value != nil {
		(*win32runtime.IUnknown)(value).Release()
	}
}

func isCapturePending(err error) bool {
	var hresult win32runtime.HRESULT
	return errors.As(err, &hresult) && uint32(hresult) == 0x8000000a
}
